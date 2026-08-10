package consensus

import (
	"context"
	"log"
	"math/big"
	"sort"
	"sync"
	"time"

	"github.com/imattau/frg/core/keys"
	"github.com/imattau/frg/core/leader"
	"github.com/imattau/frg/core/p2p"
	"github.com/imattau/frg/core/staking"
	"github.com/imattau/frg/core/statemachine"
	"github.com/imattau/frg/core/tx"
)

// Phase represents a consensus round phase.
type Phase uint8

const (
	PhasePropose   Phase = 1
	PhasePrevote   Phase = 2
	PhasePrecommit Phase = 3
	PhaseCommit    Phase = 4
)

// EngineStatus is a snapshot of the current consensus state.
type EngineStatus struct {
	Height uint64
	Round  uint32
	Phase  Phase
}

// RoundState holds the mutable state for one consensus round.
type RoundState struct {
	Height          uint64
	Round           uint32
	Phase           Phase
	Proposal        *BlockProposal
	Prevotes        map[[32]byte]Vote // validatorPK → vote
	Precommits      map[[32]byte]Vote // validatorPK → vote
	LockedBlock     *[32]byte         // nil if unlocked
	LockedRound     int32             // -1 if unlocked
	LastAttestation AttestationSet
}

// NewRoundState creates a fresh RoundState for the given height.
func NewRoundState(height uint64) *RoundState {
	return &RoundState{
		Height:      height,
		Round:       0,
		Phase:       PhasePropose,
		Prevotes:    make(map[[32]byte]Vote),
		Precommits:  make(map[[32]byte]Vote),
		LockedRound: -1,
	}
}

// IncrementRound advances to the next round, clearing proposal and votes.
func (rs *RoundState) IncrementRound() {
	rs.Round++
	rs.Phase = PhasePropose
	rs.Proposal = nil
	rs.Prevotes = make(map[[32]byte]Vote)
	rs.Precommits = make(map[[32]byte]Vote)
}

// Lock sets the locked block and round.
func (rs *RoundState) Lock(blockHash [32]byte, round uint32) {
	h := blockHash
	rs.LockedBlock = &h
	rs.LockedRound = int32(round)
}

// Unlock clears the lock.
func (rs *RoundState) Unlock() {
	rs.LockedBlock = nil
	rs.LockedRound = -1
}

// TimeoutConfig holds phase timeout durations.
type TimeoutConfig struct {
	ProposeDelay time.Duration
	Propose      time.Duration
	Prevote      time.Duration
	Precommit    time.Duration
}

// DefaultTimeouts returns production-ready timeout values.
func DefaultTimeouts() TimeoutConfig {
	return TimeoutConfig{
		ProposeDelay: 500 * time.Millisecond,
		Propose:      3 * time.Second,
		Prevote:      3 * time.Second,
		Precommit:    3 * time.Second,
	}
}

// Engine drives the BFT consensus round state machine.
type Engine struct {
	kp       *keys.Keypair
	staking  *staking.Store
	sm       *statemachine.StateMachine
	p2p      *p2p.Node
	proposer Proposer
	timeouts TimeoutConfig
	stopCh   chan struct{}
	stopOnce sync.Once
	chainID  string
	mu       sync.RWMutex
	status   EngineStatus
	// Signals "a delayed self-proposal is ready to broadcast" from a
	// time.AfterFunc callback (which runs on its own goroutine, per the Go
	// stdlib) back to Start's select loop, so broadcastProposal/handleVote
	// -- and the RoundState maps they mutate (rs.Prevotes/rs.Precommits)
	// -- only ever run on Start's single goroutine. Calling them directly
	// from an AfterFunc callback used to race Start's own concurrent
	// access to those maps, crashing with "fatal error: concurrent map
	// writes" whenever the callback fired during active voting instead of
	// into a quiet gap -- rare before restartRound (see its own docs)
	// actually worked, common once retried rounds made self-proposals
	// fire back-to-back. Buffered 1 + non-blocking send in every sender:
	// at most one pending self-proposal signal matters at a time, and a
	// timer goroutine must never block on this.
	proposeReady chan struct{}
}

// Proposer defines the interface for building and finalizing proposals.
type Proposer interface {
	Propose(height uint64, round uint32, prevAttest AttestationSet) (*BlockProposal, error)
	OnCommit(height uint64, txs []*tx.Tx)
}

type RejectingProposer interface {
	OnReject(height uint64, txs []*tx.Tx)
}

type StateRootProposer interface {
	ProposeForState(height uint64, round uint32, prevAttest AttestationSet, prevRoot [32]byte) (*BlockProposal, error)
}

// New creates a new consensus Engine.
func New(kp *keys.Keypair, s *staking.Store, sm *statemachine.StateMachine, n *p2p.Node, proposer Proposer, cfg TimeoutConfig) *Engine {
	return NewWithChainID(kp, s, sm, n, proposer, cfg, tx.DefaultChainID)
}

// NewWithChainID creates an engine whose signed messages are chain-bound.
func NewWithChainID(kp *keys.Keypair, s *staking.Store, sm *statemachine.StateMachine, n *p2p.Node, proposer Proposer, cfg TimeoutConfig, chainID string) *Engine {
	if chainID == "" {
		chainID = tx.DefaultChainID
	}
	return &Engine{
		kp:           kp,
		staking:      s,
		sm:           sm,
		p2p:          n,
		proposer:     proposer,
		timeouts:     cfg,
		stopCh:       make(chan struct{}),
		chainID:      chainID,
		proposeReady: make(chan struct{}, 1),
	}
}

// Status returns the latest consensus snapshot.
func (e *Engine) Status() EngineStatus {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.status
}

func (e *Engine) setStatus(rs *RoundState) {
	e.mu.Lock()
	e.status = EngineStatus{
		Height: rs.Height,
		Round:  rs.Round,
		Phase:  rs.Phase,
	}
	e.mu.Unlock()
}

// Start begins the consensus event loop. Blocks until ctx is cancelled or Stop is called.
func (e *Engine) Start(ctx context.Context) error {
	height, err := e.sm.CurrentHeight()
	if err != nil {
		return err
	}
	rs := NewRoundState(height + 1)
	if raw, loadErr := e.sm.LoadConsensusState(); loadErr == nil && len(raw) > 0 {
		if restored, decodeErr := decodeRoundState(raw, e.chainID); decodeErr == nil && restored.Height == height+1 {
			rs = restored
		}
	}
	e.setStatus(rs)

	validators, stakes, err := e.staking.BondedAmounts()
	if err != nil {
		return err
	}

	voteSub := e.p2p.SubscribeVotes()
	proposalSub := e.p2p.SubscribeBlockHeaders()

	proposeTimer := time.NewTimer(e.timeouts.Propose)
	prevoteTimer := time.NewTimer(0)
	prevoteTimer.Stop()
	precommitTimer := time.NewTimer(0)
	precommitTimer.Stop()

	// Restore the active phase timers and schedule a proposal only when needed.
	prevRoot := e.prevStateRoot()
	proposerPK, _ := leader.SkipProposer(prevRoot, rs.Height, validators, rs.Round)
	if rs.Phase == PhasePrevote {
		proposeTimer.Stop()
		prevoteTimer.Reset(e.timeouts.Prevote)
	} else if rs.Phase == PhasePrecommit {
		proposeTimer.Stop()
		precommitTimer.Reset(e.timeouts.Precommit)
	} else if rs.Phase == PhaseCommit {
		proposeTimer.Stop()
		blockHash, hasQuorum := e.quorumBlock(rs.Precommits, validators, stakes)
		if hasQuorum && blockHash != [32]byte{} {
			if e.commit(rs, blockHash) {
				rs.LastAttestation = attestationFromPrecommits(rs.Precommits, blockHash)
				validators, stakes, err = e.staking.BondedAmounts()
				if err != nil {
					return err
				}
				e.startNextRound(rs, validators, stakes, proposeTimer, prevoteTimer, precommitTimer)
			} else {
				e.rejectProposal(rs)
				if err := e.restartRound(rs, validators, stakes, proposeTimer, prevoteTimer, precommitTimer); err != nil {
					return err
				}
			}
		}
	} else if proposerPK == e.kp.PublicKey {
		time.AfterFunc(e.timeouts.ProposeDelay, func() {
			select {
			case e.proposeReady <- struct{}{}:
			default:
			}
		})
	}

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-e.stopCh:
			return nil

		case rawVote, ok := <-voteSub:
			if !ok {
				return nil
			}
			v, err := DeserializeVote(rawVote)
			if err != nil {
				continue
			}
			e.handleVote(rs, v, validators, stakes, proposeTimer, prevoteTimer, precommitTimer)

		case rawProposal, ok := <-proposalSub:
			if !ok {
				return nil
			}
			p := e.parseProposal(rawProposal)
			if p == nil {
				continue
			}
			e.handleProposal(rs, p, validators, stakes, proposeTimer, prevoteTimer, precommitTimer)

		case <-proposeTimer.C:
			e.onProposeTimeout(rs, validators, stakes, prevoteTimer, precommitTimer, proposeTimer)

		case <-prevoteTimer.C:
			e.onPrevoteTimeout(rs, validators, stakes, precommitTimer, proposeTimer, prevoteTimer)

		case <-precommitTimer.C:
			rs.IncrementRound()
			e.setStatus(rs)
			prevRoot = e.prevStateRoot()
			proposerPK, _ = leader.SkipProposer(prevRoot, rs.Height, validators, rs.Round)
			proposeTimer.Reset(e.timeouts.Propose)
			if proposerPK == e.kp.PublicKey {
				e.broadcastProposal(rs, prevRoot, validators, stakes, proposeTimer, prevoteTimer, precommitTimer)
			}
			persistRoundState(e.sm, rs)

		case <-e.proposeReady:
			// A delayed self-proposal from startNextRound or the resume-
			// on-Propose case above (see proposeReady's field docs). Guard
			// on Phase: by the time this fires, an externally-received
			// proposal may already have moved rs past PhasePropose, in
			// which case there's nothing to do.
			if rs.Phase == PhasePropose {
				prevRoot = e.prevStateRoot()
				proposerPK, _ = leader.SkipProposer(prevRoot, rs.Height, validators, rs.Round)
				if proposerPK == e.kp.PublicKey {
					e.broadcastProposal(rs, prevRoot, validators, stakes, proposeTimer, prevoteTimer, precommitTimer)
				}
			}
		}
	}
}

// Stop signals the engine to halt.
func (e *Engine) Stop() {
	e.stopOnce.Do(func() { close(e.stopCh) })
}

func (e *Engine) prevStateRoot() [32]byte {
	if e.sm == nil {
		return [32]byte{}
	}
	root, err := e.sm.CurrentStateRoot()
	if err != nil {
		return [32]byte{}
	}
	return root
}

func (e *Engine) broadcastProposal(rs *RoundState, prevRoot [32]byte, validators [][32]byte, stakes []*big.Int, proposeTimer, prevoteTimer, precommitTimer *time.Timer) {
	if e.proposer == nil {
		return
	}
	// For now, we don't have a way to get the last height's precommits easily here
	// In a full implementation, we'd store the last round's precommits.
	var p *BlockProposal
	var err error
	if proposer, ok := e.proposer.(StateRootProposer); ok {
		p, err = proposer.ProposeForState(rs.Height, rs.Round, rs.LastAttestation, prevRoot)
	} else {
		p, err = e.proposer.Propose(rs.Height, rs.Round, rs.LastAttestation)
	}
	if err != nil {
		return
	}
	if rs.Phase != PhasePropose {
		return
	}
	data, err := SerializeProposal(p)
	if err != nil {
		// rs.Phase must stay PhasePropose here: if we advanced it before
		// this could fail, a serialization error would strand the round in
		// PhasePrevote with no prevote ever cast and no timer armed to
		// recover it, since onProposeTimeout's phase guard would then
		// silently no-op forever. Leaving Phase untouched lets the
		// propose-timeout fallback fire normally.
		return
	}
	rs.Proposal = p
	rs.Phase = PhasePrevote
	e.setStatus(rs)
	persistRoundState(e.sm, rs)
	_ = e.p2p.BroadcastBlockHeader(data)
	if vote := e.broadcastPrevote(rs, p.BlockHashForChain(e.chainID)); vote != nil {
		e.handleVote(rs, vote, validators, stakes, proposeTimer, prevoteTimer, precommitTimer)
	}
}

func (e *Engine) handleVote(rs *RoundState, v *Vote, validators [][32]byte, stakes []*big.Int,
	proposeTimer, prevoteTimer, precommitTimer *time.Timer) {


	if v.Height != rs.Height || v.Round != rs.Round {
		return
	}
	if !VerifyVoteForChain(v, e.chainID) {
		return
	}
	validator := false
	for _, pk := range validators {
		if pk == v.ValidatorPK {
			validator = true
			break
		}
	}
	if !validator {
		return
	}

	switch v.Type {
	case VotePrevote:
		if _, dup := rs.Prevotes[v.ValidatorPK]; dup {
			return
		}
		rs.Prevotes[v.ValidatorPK] = *v
		persistRoundState(e.sm, rs)
		if QuorumReached(rs.Prevotes, validators, stakes) && rs.Phase == PhasePrevote {
			rs.Phase = PhasePrecommit
			e.setStatus(rs)
			prevoteTimer.Stop()
			precommitTimer.Reset(e.timeouts.Precommit)
			// Determine if quorum is on a specific block or nil
			blockHash, hasQuorum := e.quorumBlock(rs.Prevotes, validators, stakes)
			if hasQuorum {
				rs.Lock(blockHash, rs.Round)
				if vote := e.broadcastPrecommit(rs, blockHash); vote != nil {
					e.handleVote(rs, vote, validators, stakes, proposeTimer, prevoteTimer, precommitTimer)
				}
			} else {
				if vote := e.broadcastPrecommit(rs, [32]byte{}); vote != nil {
					e.handleVote(rs, vote, validators, stakes, proposeTimer, prevoteTimer, precommitTimer)
				}
			}
		}

	case VotePrecommit:
		if _, dup := rs.Precommits[v.ValidatorPK]; dup {
			return
		}
		rs.Precommits[v.ValidatorPK] = *v
		persistRoundState(e.sm, rs)
		if QuorumReached(rs.Precommits, validators, stakes) && rs.Phase == PhasePrecommit {
			blockHash, hasQuorum := e.quorumBlock(rs.Precommits, validators, stakes)
			if hasQuorum && blockHash != [32]byte{} {
				precommitTimer.Stop()
				rs.Phase = PhaseCommit
				e.setStatus(rs)
				commitOK := e.commit(rs, blockHash)
				if commitOK {
					rs.LastAttestation = attestationFromPrecommits(rs.Precommits, blockHash)
					var refreshErr error
					validators, stakes, refreshErr = e.staking.BondedAmounts()
					if refreshErr != nil {
						return
					}
					e.startNextRound(rs, validators, stakes, proposeTimer, prevoteTimer, precommitTimer)
				} else {
					// e.commit already logs why ApplyBlockForChain failed
					// (see its own log.Printf). Without this branch, a
					// failed commit left rs.Phase stuck at PhaseCommit
					// forever -- nothing schedules another proposal, vote,
					// or timeout, so the engine's select loop just parks
					// indefinitely and the chain halts at this height.
					// Reproduced live: a contract call whose WASM traps
					// (a deterministic, guaranteed-to-fail-again proposal)
					// wedged a real devnet exactly this way. Mirrors the
					// sibling "no quorum on a real block" case right below.
					e.rejectProposal(rs)
					if err := e.restartRound(rs, validators, stakes, proposeTimer, prevoteTimer, precommitTimer); err != nil {
						return
					}
				}
			} else {
				e.rejectProposal(rs)
				if err := e.restartRound(rs, validators, stakes, proposeTimer, prevoteTimer, precommitTimer); err != nil {
					return
				}
			}
		}
	}
}

func (e *Engine) handleProposal(rs *RoundState, p *BlockProposal, validators [][32]byte, stakes []*big.Int,
	proposeTimer, prevoteTimer, precommitTimer *time.Timer) {

	if p.Height != rs.Height || p.Round != rs.Round {
		return
	}
	if p.PrevStateRoot != e.prevStateRoot() {
		return
	}
	if rs.Phase != PhasePropose || rs.Proposal != nil {
		return
	}
	expected, err := leader.SkipProposer(e.prevStateRoot(), rs.Height, validators, rs.Round)
	if err != nil || p.ProposerPK != expected {
		return
	}
	for _, t := range p.Txs {
		if err := t.VerifySigsForChain(e.chainID); err != nil {
			return
		}
	}
	if len(p.PrevAttestations.Votes) > 0 {
		if err := VerifyAttestationForChain(&p.PrevAttestations, validators, stakes, e.chainID); err != nil {
			return
		}
	}
	rs.Proposal = p
	rs.Phase = PhasePrevote
	e.setStatus(rs)
	proposeTimer.Stop()

	blockHash := p.BlockHashForChain(e.chainID)
	prevoteTimer.Reset(e.timeouts.Prevote)

	// Lock rule: if locked on a different block, prevote nil unless we see 2/3+ for new block.
	if rs.LockedBlock != nil && *rs.LockedBlock != blockHash {
		if vote := e.broadcastPrevote(rs, [32]byte{}); vote != nil {
			e.handleVote(rs, vote, validators, stakes, proposeTimer, prevoteTimer, precommitTimer)
		}
	} else {
		if vote := e.broadcastPrevote(rs, blockHash); vote != nil {
			e.handleVote(rs, vote, validators, stakes, proposeTimer, prevoteTimer, precommitTimer)
		}
	}
	persistRoundState(e.sm, rs)
}

func (e *Engine) onProposeTimeout(rs *RoundState, validators [][32]byte, stakes []*big.Int, prevoteTimer, precommitTimer, proposeTimer *time.Timer) {
	if rs.Phase != PhasePropose {
		return
	}
	rs.Phase = PhasePrevote
	e.setStatus(rs)
	prevoteTimer.Reset(e.timeouts.Prevote)
	if vote := e.broadcastPrevote(rs, [32]byte{}); vote != nil {
		e.handleVote(rs, vote, validators, stakes, proposeTimer, prevoteTimer, precommitTimer)
	}
	persistRoundState(e.sm, rs)
}

func (e *Engine) onPrevoteTimeout(rs *RoundState, validators [][32]byte, stakes []*big.Int, precommitTimer, proposeTimer, prevoteTimer *time.Timer) {
	if rs.Phase != PhasePrevote {
		return
	}
	rs.Phase = PhasePrecommit
	e.setStatus(rs)
	precommitTimer.Reset(e.timeouts.Precommit)
	if vote := e.broadcastPrecommit(rs, [32]byte{}); vote != nil {
		e.handleVote(rs, vote, validators, stakes, proposeTimer, prevoteTimer, precommitTimer)
	}
	persistRoundState(e.sm, rs)
}

func (e *Engine) broadcastPrevote(rs *RoundState, blockHash [32]byte) *Vote {
	if !e.sm.RecordConsensusVote(rs.Height, rs.Round, uint8(VotePrevote), blockHash) {
		return nil
	}
	v := &Vote{
		Type: VotePrevote, Height: rs.Height, Round: rs.Round,
		BlockHash: blockHash, ValidatorPK: e.kp.PublicKey,
	}
	body := VoteSignBytesForChain(v, e.chainID)
	sig, err := e.kp.Sign(body)
	if err != nil {
		return nil
	}
	v.Sig = sig
	data, err := v.Serialize()
	if err != nil {
		return nil
	}
	_ = e.p2p.BroadcastVote(data)
	return v
}

func (e *Engine) broadcastPrecommit(rs *RoundState, blockHash [32]byte) *Vote {
	if !e.sm.RecordConsensusVote(rs.Height, rs.Round, uint8(VotePrecommit), blockHash) {
		return nil
	}
	v := &Vote{
		Type: VotePrecommit, Height: rs.Height, Round: rs.Round,
		BlockHash: blockHash, ValidatorPK: e.kp.PublicKey,
	}
	body := VoteSignBytesForChain(v, e.chainID)
	sig, err := e.kp.Sign(body)
	if err != nil {
		return nil
	}
	v.Sig = sig
	data, err := v.Serialize()
	if err != nil {
		return nil
	}
	_ = e.p2p.BroadcastVote(data)
	return v
}

func (e *Engine) commit(rs *RoundState, blockHash [32]byte) bool {
	if rs.Proposal == nil || rs.Proposal.BlockHashForChain(e.chainID) != blockHash {
		return false
	}
	b := &statemachine.Block{
		Height:         rs.Height,
		Txs:            rs.Proposal.Txs,
		ProposerPubKey: rs.Proposal.ProposerPK,
		PrevStateRoot:  rs.Proposal.PrevStateRoot,
	}
	proposalBytes, err := SerializeProposal(rs.Proposal)
	if err != nil {
		log.Printf("consensus: failed to serialize committed block height=%d round=%d: %v", rs.Height, rs.Round, err)
		return false
	}
	b.ProposalBytes = proposalBytes
	_, err = e.sm.ApplyBlockForChain(b, e.chainID)
	if err != nil {
		log.Printf("consensus: failed to apply committed block height=%d round=%d: %v", rs.Height, rs.Round, err)
		return false
	}
	_ = e.sm.ClearConsensusState()
	if e.proposer != nil {
		e.proposer.OnCommit(rs.Height, rs.Proposal.Txs)
	}
	return true
}

func (e *Engine) rejectProposal(rs *RoundState) {
	if rejecter, ok := e.proposer.(RejectingProposer); ok && rs.Proposal != nil {
		rejecter.OnReject(rs.Height, rs.Proposal.Txs)
	}
}

func (e *Engine) restartRound(rs *RoundState, validators [][32]byte, stakes []*big.Int, proposeTimer, prevoteTimer, precommitTimer *time.Timer) error {
	var err error
	validators, stakes, err = e.staking.BondedAmounts()
	if err != nil {
		return err
	}
	// Retries the SAME height with a new round -- NOT startNextRound, which
	// advances rs.Height. A commit failure means ApplyBlockForChain rolled
	// back and the statemachine's real committed height is unchanged, so
	// bumping rs.Height here desyncs consensus's view of the current height
	// from the statemachine's: every subsequent proposal would then be built
	// for a height ApplyBlockForChain's `cur+1` check can never accept, and
	// since that mismatch is caught before touching votes at all, nothing
	// ever logs it or reaches another commit attempt -- the engine just
	// parks in its select loop forever, permanently stalling the chain.
	// Mirrors the precommitTimer-timeout case above, the other "retry this
	// height" path, which already gets this right.
	rs.Unlock() // the rejected proposal just failed deterministically; don't let a lock force re-proposing the exact same bad block.
	rs.IncrementRound()
	e.setStatus(rs)
	prevRoot := e.prevStateRoot()
	proposerPK, _ := leader.SkipProposer(prevRoot, rs.Height, validators, rs.Round)
	proposeTimer.Reset(e.timeouts.Propose)
	if proposerPK == e.kp.PublicKey {
		e.broadcastProposal(rs, prevRoot, validators, stakes, proposeTimer, prevoteTimer, precommitTimer)
	}
	persistRoundState(e.sm, rs)
	return nil
}

func attestationFromPrecommits(votes map[[32]byte]Vote, blockHash [32]byte) AttestationSet {
	attestation := AttestationSet{BlockHash: blockHash}
	for _, vote := range votes {
		if vote.Type == VotePrecommit && vote.BlockHash == blockHash {
			attestation.Votes = append(attestation.Votes, vote)
		}
	}
	sort.Slice(attestation.Votes, func(i, j int) bool {
		return string(attestation.Votes[i].ValidatorPK[:]) < string(attestation.Votes[j].ValidatorPK[:])
	})
	if len(attestation.Votes) > 0 {
		attestation.Height = attestation.Votes[0].Height
		attestation.Round = attestation.Votes[0].Round
	}
	return attestation
}

func (e *Engine) startNextRound(rs *RoundState, validators [][32]byte, stakes []*big.Int, proposeTimer, prevoteTimer, precommitTimer *time.Timer) {
	rs.Height++
	rs.Round = 0
	rs.Phase = PhasePropose
	rs.Proposal = nil
	rs.Prevotes = make(map[[32]byte]Vote)
	rs.Precommits = make(map[[32]byte]Vote)
	rs.LockedBlock = nil
	e.setStatus(rs)

	prevRoot := e.prevStateRoot()
	proposerPK, _ := leader.SkipProposer(prevRoot, rs.Height, validators, rs.Round)
	proposeTimer.Reset(e.timeouts.Propose)
	if proposerPK == e.kp.PublicKey {
		// See proposeReady's field docs: must not call broadcastProposal
		// directly from this callback's own goroutine.
		time.AfterFunc(e.timeouts.ProposeDelay, func() {
			select {
			case e.proposeReady <- struct{}{}:
			default:
			}
		})
	}
	persistRoundState(e.sm, rs)
}

func (e *Engine) quorumBlock(votes map[[32]byte]Vote, validators [][32]byte, stakes []*big.Int) ([32]byte, bool) {
	stakeMap := make(map[[32]byte]*big.Int)
	totalStake := new(big.Int)
	for i, pk := range validators {
		stakeMap[pk] = stakes[i]
		totalStake.Add(totalStake, stakes[i])
	}

	tally := make(map[[32]byte]*big.Int)
	for _, v := range votes {
		if stake, ok := stakeMap[v.ValidatorPK]; ok {
			if tally[v.BlockHash] == nil {
				tally[v.BlockHash] = new(big.Int)
			}
			tally[v.BlockHash].Add(tally[v.BlockHash], stake)
		}
	}
	rhs := new(big.Int).Mul(totalStake, big.NewInt(2))
	for blockHash, sum := range tally {
		lhs := new(big.Int).Mul(sum, big.NewInt(3))
		if lhs.Cmp(rhs) > 0 {
			return blockHash, true
		}
	}
	return [32]byte{}, false
}

func (e *Engine) parseProposal(raw []byte) *BlockProposal {
	p, err := DeserializeProposal(raw)
	if err != nil {
		return nil
	}
	if !keys.Verify(p.ProposerPK, ProposalSignBytesForChain(p, e.chainID), p.ProposerSig) {
		return nil
	}
	return p
}
