package consensus

import (
    "context"
    "math/big"
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

// RoundState holds the mutable state for one consensus round.
type RoundState struct {
    Height     uint64
    Round      uint32
    Phase      Phase
    Proposal   *BlockProposal
    Prevotes   map[[32]byte]Vote // validatorPK → vote
    Precommits map[[32]byte]Vote // validatorPK → vote
    LockedBlock *[32]byte        // nil if unlocked
    LockedRound int32            // -1 if unlocked
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
    Propose    time.Duration
    Prevote    time.Duration
    Precommit  time.Duration
}

// DefaultTimeouts returns production-ready timeout values.
func DefaultTimeouts() TimeoutConfig {
    return TimeoutConfig{
        Propose:   3 * time.Second,
        Prevote:   3 * time.Second,
        Precommit: 3 * time.Second,
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
}

// Proposer defines the interface for building and finalizing proposals.
type Proposer interface {
    Propose(height uint64, round uint32, prevAttest AttestationSet) (*BlockProposal, error)
    OnCommit(height uint64, txs []*tx.Tx)
}

// New creates a new consensus Engine.
func New(kp *keys.Keypair, s *staking.Store, sm *statemachine.StateMachine, n *p2p.Node, proposer Proposer, cfg TimeoutConfig) *Engine {
    return &Engine{
        kp:       kp,
        staking:  s,
        sm:       sm,
        p2p:      n,
        proposer: proposer,
        timeouts: cfg,
        stopCh:   make(chan struct{}),
    }
}

// Start begins the consensus event loop. Blocks until ctx is cancelled or Stop is called.
func (e *Engine) Start(ctx context.Context) error {
    height, err := e.sm.CurrentHeight()
    if err != nil {
        return err
    }
    rs := NewRoundState(height + 1)

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

    // Enter propose: if we are the proposer, build and broadcast.
    prevRoot := e.prevStateRoot()
    proposerPK, _ := leader.SkipProposer(prevRoot, rs.Height, validators, rs.Round)
    if proposerPK == e.kp.PublicKey {
        e.broadcastProposal(rs, prevRoot)
    }

    for {
        select {
        case <-ctx.Done():
            return nil
        case <-e.stopCh:
            return nil

        case rawVote := <-voteSub:
            v, err := DeserializeVote(rawVote)
            if err != nil {
                continue
            }
            e.handleVote(rs, v, validators, stakes, proposeTimer, prevoteTimer, precommitTimer)

        case rawProposal := <-proposalSub:
            p := e.parseProposal(rawProposal)
            if p == nil {
                continue
            }
            e.handleProposal(rs, p, validators, stakes, proposeTimer, prevoteTimer)

        case <-proposeTimer.C:
            e.onProposeTimeout(rs, validators, prevoteTimer)

        case <-prevoteTimer.C:
            e.onPrevoteTimeout(rs, validators, stakes, precommitTimer)

        case <-precommitTimer.C:
            rs.IncrementRound()
            prevRoot = e.prevStateRoot()
            proposerPK, _ = leader.SkipProposer(prevRoot, rs.Height, validators, rs.Round)
            proposeTimer.Reset(e.timeouts.Propose)
            if proposerPK == e.kp.PublicKey {
                e.broadcastProposal(rs, prevRoot)
            }
        }
    }
}

// Stop signals the engine to halt.
func (e *Engine) Stop() {
    close(e.stopCh)
}

func (e *Engine) prevStateRoot() [32]byte {
    // In a full implementation, retrieve from state machine.
    // Returns zero value for height 1 (genesis predecessor).
    return [32]byte{}
}

func (e *Engine) broadcastProposal(rs *RoundState, prevRoot [32]byte) {
    if e.proposer == nil {
        return
    }
    // For now, we don't have a way to get the last height's precommits easily here
    // In a full implementation, we'd store the last round's precommits.
    p, err := e.proposer.Propose(rs.Height, rs.Round, AttestationSet{})
    if err != nil {
        return
    }
    data, err := SerializeProposal(p)
    if err != nil {
        return
    }
    _ = e.p2p.BroadcastBlockHeader(data)
}

func (e *Engine) handleVote(rs *RoundState, v *Vote, validators [][32]byte, stakes []*big.Int,
    proposeTimer, prevoteTimer, precommitTimer *time.Timer) {

    if v.Height != rs.Height || v.Round != rs.Round {
        return
    }
    if !VerifyVote(v) {
        return
    }

    switch v.Type {
    case VotePrevote:
        if _, dup := rs.Prevotes[v.ValidatorPK]; dup {
            return
        }
        rs.Prevotes[v.ValidatorPK] = *v
        if QuorumReached(rs.Prevotes, validators, stakes) && rs.Phase == PhasePrevote {
            rs.Phase = PhasePrecommit
            prevoteTimer.Stop()
            // Determine if quorum is on a specific block or nil
            blockHash, hasQuorum := e.quorumBlock(rs.Prevotes, validators, stakes)
            if hasQuorum {
                rs.Lock(blockHash, rs.Round)
                e.broadcastPrecommit(rs, blockHash)
            } else {
                e.broadcastPrecommit(rs, [32]byte{})
            }
            precommitTimer.Reset(e.timeouts.Precommit)
        }

    case VotePrecommit:
        if _, dup := rs.Precommits[v.ValidatorPK]; dup {
            return
        }
        rs.Precommits[v.ValidatorPK] = *v
        if QuorumReached(rs.Precommits, validators, stakes) && rs.Phase == PhasePrecommit {
            blockHash, hasQuorum := e.quorumBlock(rs.Precommits, validators, stakes)
            if hasQuorum && blockHash != [32]byte{} {
                precommitTimer.Stop()
                rs.Phase = PhaseCommit
                e.commit(rs, blockHash)
            }
        }
    }
}

func (e *Engine) handleProposal(rs *RoundState, p *BlockProposal, validators [][32]byte, stakes []*big.Int,
    proposeTimer, prevoteTimer *time.Timer) {

    if p.Height != rs.Height || p.Round != rs.Round {
        return
    }
    if rs.Phase != PhasePropose || rs.Proposal != nil {
        return
    }
    rs.Proposal = p
    rs.Phase = PhasePrevote
    proposeTimer.Stop()

    blockHash := p.BlockHash()

    // Lock rule: if locked on a different block, prevote nil unless we see 2/3+ for new block.
    if rs.LockedBlock != nil && *rs.LockedBlock != blockHash {
        e.broadcastPrevote(rs, [32]byte{})
    } else {
        e.broadcastPrevote(rs, blockHash)
    }
    prevoteTimer.Reset(e.timeouts.Prevote)
}

func (e *Engine) onProposeTimeout(rs *RoundState, validators [][32]byte, prevoteTimer *time.Timer) {
    if rs.Phase != PhasePropose {
        return
    }
    rs.Phase = PhasePrevote
    e.broadcastPrevote(rs, [32]byte{}) // nil prevote
    prevoteTimer.Reset(e.timeouts.Prevote)
}

func (e *Engine) onPrevoteTimeout(rs *RoundState, validators [][32]byte, stakes []*big.Int, precommitTimer *time.Timer) {
    if rs.Phase != PhasePrevote {
        return
    }
    rs.Phase = PhasePrecommit
    e.broadcastPrecommit(rs, [32]byte{}) // nil precommit
    precommitTimer.Reset(e.timeouts.Precommit)
}

func (e *Engine) broadcastPrevote(rs *RoundState, blockHash [32]byte) {
    v := &Vote{
        Type: VotePrevote, Height: rs.Height, Round: rs.Round,
        BlockHash: blockHash, ValidatorPK: e.kp.PublicKey,
    }
    body := VoteSignBytes(v)
    sig, err := e.kp.Sign(body)
    if err != nil {
        return
    }
    v.Sig = sig
    data, err := v.Serialize()
    if err != nil {
        return
    }
    _ = e.p2p.BroadcastVote(data)
}

func (e *Engine) broadcastPrecommit(rs *RoundState, blockHash [32]byte) {
    v := &Vote{
        Type: VotePrecommit, Height: rs.Height, Round: rs.Round,
        BlockHash: blockHash, ValidatorPK: e.kp.PublicKey,
    }
    body := VoteSignBytes(v)
    sig, err := e.kp.Sign(body)
    if err != nil {
        return
    }
    v.Sig = sig
    data, err := v.Serialize()
    if err != nil {
        return
    }
    _ = e.p2p.BroadcastVote(data)
}

func (e *Engine) commit(rs *RoundState, blockHash [32]byte) {
    if rs.Proposal == nil || rs.Proposal.BlockHash() != blockHash {
        return
    }
    b := &statemachine.Block{
        Height:         rs.Height,
        Txs:            rs.Proposal.Txs,
        ProposerPubKey: rs.Proposal.ProposerPK,
    }
    _, err := e.sm.ApplyBlock(b)
    if err != nil {
        return
    }
    if e.proposer != nil {
        e.proposer.OnCommit(rs.Height, rs.Proposal.Txs)
    }
    // Advance to next height — block production loop will handle tx selection.
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
    if !keys.Verify(p.ProposerPK, ProposalSignBytes(p), p.ProposerSig) {
        return nil
    }
    return p
}
