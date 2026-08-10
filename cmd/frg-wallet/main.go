package main

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"math/big"
	"net/http"
	"os"
	"time"

	"github.com/imattau/frg/core/denom"
	"github.com/imattau/frg/core/tx"
	frgpb "github.com/imattau/frg/proto"
	"github.com/imattau/frg/wallet"
)

type server struct {
	w         *wallet.Wallet
	faucetURL string
}

type pubkeyResponse struct {
	Pubkey  string `json:"pubkey"`
	ChainID string `json:"chain_id"`
}

type accountResponse struct {
	Pubkey     string `json:"pubkey"`
	Balance    string `json:"balance"`
	BalanceFRG string `json:"balance_frg"`
	Nonce      uint64 `json:"nonce"`
}

type validatorEntry struct {
	Pubkey  string `json:"pubkey"`
	Bond    string `json:"bond"`
	BondFRG string `json:"bond_frg"`
}

type validatorsResponse struct {
	Validators []validatorEntry `json:"validators"`
}

type transferRequest struct {
	To           string `json:"to"`
	Amount       string `json:"amount"`
	AmountQuanta string `json:"amount_quanta"`
}

type bondRequest struct {
	Amount       string `json:"amount"`
	AmountQuanta string `json:"amount_quanta"`
}

type missedDeadlineReportRequest struct {
	MissedHeight   uint64 `json:"missed_height"`
	MissedProposer string `json:"missed_proposer"`
	SkipIndex      uint32 `json:"skip_index"`
}

type contractDeployRequest struct {
	WasmHex     string `json:"wasm_hex"`
	Value       string `json:"value"`
	ValueQuanta string `json:"value_quanta"`
}

type contractCallRequest struct {
	ContractAddress string `json:"contract_address"`
	CallDataHex     string `json:"call_data_hex"`
	Function        string `json:"function"`
	Value           string `json:"value"`
	ValueQuanta     string `json:"value_quanta"`
}

type contractAddressResponse struct {
	ContractAddress string `json:"contract_address"`
}

type contractStateResponse struct {
	ContractAddress string `json:"contract_address"`
	Exists          bool   `json:"exists"`
	StateRoot       string `json:"state_root,omitempty"`
	Key             string `json:"key,omitempty"`
	Found           bool   `json:"found"`
	Value           string `json:"value,omitempty"`
}

type faucetRequest struct {
	Pubkey string `json:"pubkey"`
}

type errorResponse struct {
	Error string `json:"error"`
}

func main() {
	listenAddr := flag.String("listen", "127.0.0.1:8090", "HTTP listen address")
	nodeAddr := flag.String("node", "127.0.0.1:50051", "FRG node gRPC address")
	keyPath := flag.String("key", "frg-wallet.key", "wallet seed/private key file")
	chainID := flag.String("chain-id", tx.DefaultChainID, "chain ID for transaction signatures")
	createKey := flag.Bool("create-key", false, "create --key if it does not exist")
	faucetURL := flag.String("faucet-url", "", "optional faucet URL for POST /faucet")
	flag.Parse()

	kp, err := wallet.LoadKeypair(*keyPath)
	if err != nil {
		if !*createKey || !errors.Is(err, os.ErrNotExist) {
			log.Fatalf("load key: %v", err)
		}
		kp, err = wallet.GenerateKeypair()
		if err != nil {
			log.Fatalf("generate key: %v", err)
		}
		if err := wallet.SaveSeed(*keyPath, kp); err != nil {
			log.Fatalf("save key: %v", err)
		}
		log.Printf("created wallet key at %s", *keyPath)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client, err := wallet.Dial(ctx, *nodeAddr, kp, *chainID)
	if err != nil {
		log.Fatalf("dial node: %v", err)
	}
	defer client.Close()

	s := &server{w: client.Wallet, faucetURL: *faucetURL}
	mux := http.NewServeMux()
	mux.HandleFunc("/health", s.handleHealth)
	mux.HandleFunc("/pubkey", s.handlePubkey)
	mux.HandleFunc("/account", s.handleAccount)
	mux.HandleFunc("/balance", s.handleAccount)
	mux.HandleFunc("/status", s.handleStatus)
	mux.HandleFunc("/validators", s.handleValidators)
	mux.HandleFunc("/transfer", s.handleTransfer)
	mux.HandleFunc("/bond", s.handleBond)
	mux.HandleFunc("/unbond", s.handleUnbond)
	mux.HandleFunc("/finalize-unbond", s.handleFinalizeUnbond)
	mux.HandleFunc("/claim-rewards", s.handleClaimRewards)
	mux.HandleFunc("/miss-evidence", s.handleMissedDeadlineReport)
	mux.HandleFunc("/submit-missed-deadline-report", s.handleMissedDeadlineReport)
	mux.HandleFunc("/contracts/address", s.handleContractAddress)
	mux.HandleFunc("/contracts/state", s.handleContractState)
	mux.HandleFunc("/contracts/deploy", s.handleContractDeploy)
	mux.HandleFunc("/contracts/call", s.handleContractCall)
	mux.HandleFunc("/faucet", s.handleFaucet)

	srv := &http.Server{
		Addr:         *listenAddr,
		Handler:      mux,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 20 * time.Second,
		IdleTimeout:  60 * time.Second,
	}
	log.Printf("wallet pubkey: %s", client.PublicKeyHex())
	log.Printf("wallet API listening on http://%s (node: %s)", *listenAddr, *nodeAddr)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("serve: %v", err)
	}
}

func (s *server) handleHealth(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodGet) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *server) handlePubkey(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodGet) {
		return
	}
	writeJSON(w, http.StatusOK, pubkeyResponse{Pubkey: s.w.PublicKeyHex(), ChainID: s.w.ChainID()})
}

func (s *server) handleAccount(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodGet) {
		return
	}
	pubkey, err := s.pubkeyFromQuery(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	resp, err := s.w.Account(r.Context(), pubkey)
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, formatAccount(resp))
}

func (s *server) handleStatus(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodGet) {
		return
	}
	resp, err := s.w.Status(r.Context())
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *server) handleValidators(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodGet) {
		return
	}
	resp, err := s.w.Validators(r.Context())
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	out := validatorsResponse{Validators: make([]validatorEntry, 0, len(resp.Validators))}
	for _, v := range resp.Validators {
		if v == nil {
			continue
		}
		out.Validators = append(out.Validators, validatorEntry{
			Pubkey:  hex.EncodeToString(v.Pubkey),
			Bond:    v.Bond,
			BondFRG: formatQuantaAsFRG(v.Bond),
		})
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *server) handleTransfer(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodPost) {
		return
	}
	var req transferRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid JSON: %w", err))
		return
	}
	to, err := wallet.DecodePubKey(req.To)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	amount, err := parseAmountFields(req.Amount, req.AmountQuanta, "amount", true)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	resp, err := s.w.Transfer(r.Context(), to, amount)
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *server) handleBond(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodPost) {
		return
	}
	var req bondRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid JSON: %w", err))
		return
	}
	amount, err := parseAmountFields(req.Amount, req.AmountQuanta, "amount", true)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	resp, err := s.w.Bond(r.Context(), amount)
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *server) handleUnbond(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodPost) {
		return
	}
	resp, err := s.w.Unbond(r.Context())
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *server) handleFinalizeUnbond(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodPost) {
		return
	}
	resp, err := s.w.FinalizeUnbond(r.Context())
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *server) handleClaimRewards(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodPost) {
		return
	}
	resp, err := s.w.ClaimRewards(r.Context())
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *server) handleMissedDeadlineReport(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodPost) {
		return
	}
	var req missedDeadlineReportRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid JSON: %w", err))
		return
	}
	if req.MissedHeight == 0 {
		writeError(w, http.StatusBadRequest, fmt.Errorf("missed_height must be positive"))
		return
	}
	missedProposer, err := wallet.DecodePubKey(req.MissedProposer)
	if err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("missed_proposer: %w", err))
		return
	}
	resp, err := s.w.SubmitMissedDeadlineReport(r.Context(), req.MissedHeight, missedProposer, req.SkipIndex)
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *server) handleContractAddress(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodGet) {
		return
	}
	nonceRaw := r.URL.Query().Get("nonce")
	if nonceRaw == "" {
		acct, err := s.w.OwnAccount(r.Context())
		if err != nil {
			writeError(w, http.StatusBadGateway, err)
			return
		}
		nonceRaw = fmt.Sprintf("%d", acct.Nonce+1)
	}
	nonce, ok := new(big.Int).SetString(nonceRaw, 10)
	if !ok || nonce.Sign() <= 0 || !nonce.IsUint64() {
		writeError(w, http.StatusBadRequest, fmt.Errorf("nonce must be a positive uint64"))
		return
	}
	addr := s.w.ContractAddress(nonce.Uint64())
	writeJSON(w, http.StatusOK, contractAddressResponse{ContractAddress: hex.EncodeToString(addr[:])})
}

func (s *server) handleContractDeploy(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodPost) {
		return
	}
	var req contractDeployRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid JSON: %w", err))
		return
	}
	wasm, err := hex.DecodeString(req.WasmHex)
	if err != nil || len(wasm) == 0 {
		writeError(w, http.StatusBadRequest, fmt.Errorf("wasm_hex must be non-empty hex"))
		return
	}
	value, err := parseAmountFields(req.Value, req.ValueQuanta, "value", false)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	resp, err := s.w.DeployContract(r.Context(), wasm, value)
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *server) handleContractState(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodGet) {
		return
	}
	addrRaw := r.URL.Query().Get("contract_address")
	if addrRaw == "" {
		addrRaw = r.URL.Query().Get("address")
	}
	addr, err := wallet.DecodePubKey(addrRaw)
	if err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("contract_address must be 32-byte hex"))
		return
	}
	key, err := queryKey(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	resp, err := s.w.ContractState(r.Context(), addr, key)
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, formatContractState(resp))
}

func (s *server) handleContractCall(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodPost) {
		return
	}
	var req contractCallRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid JSON: %w", err))
		return
	}
	addr, err := wallet.DecodePubKey(req.ContractAddress)
	if err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("contract_address must be 32-byte hex"))
		return
	}
	callData, err := contractCallData(req)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	value, err := parseAmountFields(req.Value, req.ValueQuanta, "value", false)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	resp, err := s.w.CallContract(r.Context(), addr, callData, value)
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *server) handleFaucet(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodPost) {
		return
	}
	if s.faucetURL == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("--faucet-url is not configured"))
		return
	}
	var req faucetRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid JSON: %w", err))
		return
	}
	pubkey := s.w.PublicKey()
	if req.Pubkey != "" {
		var err error
		pubkey, err = wallet.DecodePubKey(req.Pubkey)
		if err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
	}
	resp, err := wallet.RequestFaucet(r.Context(), s.faucetURL, pubkey)
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *server) pubkeyFromQuery(r *http.Request) ([32]byte, error) {
	pubkeyHex := r.URL.Query().Get("pubkey")
	if pubkeyHex == "" {
		return s.w.PublicKey(), nil
	}
	return wallet.DecodePubKey(pubkeyHex)
}

func parseAmountFields(frgRaw, quantaRaw, label string, positive bool) (*big.Int, error) {
	if frgRaw != "" && quantaRaw != "" {
		return nil, fmt.Errorf("use %s or %s_quanta, not both", label, label)
	}
	if frgRaw == "" && quantaRaw == "" && !positive {
		return big.NewInt(0), nil
	}
	if frgRaw == "" && quantaRaw == "" {
		return nil, fmt.Errorf("%s is required", label)
	}
	var (
		amount *big.Int
		err    error
	)
	if quantaRaw != "" {
		amount, err = denom.ParseQuanta(quantaRaw)
	} else {
		amount, err = denom.ParseFRG(frgRaw)
	}
	if err != nil {
		return nil, fmt.Errorf("%s: %w", label, err)
	}
	if positive && amount.Sign() <= 0 {
		return nil, fmt.Errorf("%s must be positive", label)
	}
	return amount, nil
}

func contractCallData(req contractCallRequest) ([]byte, error) {
	if req.CallDataHex != "" {
		data, err := hex.DecodeString(req.CallDataHex)
		if err != nil {
			return nil, fmt.Errorf("call_data_hex must be hex")
		}
		return data, nil
	}
	if req.Function == "" {
		return []byte("call"), nil
	}
	if len(req.Function) != 4 {
		return nil, fmt.Errorf("function must be exactly 4 bytes")
	}
	return []byte(req.Function), nil
}

func queryKey(r *http.Request) ([]byte, error) {
	keyHex := r.URL.Query().Get("key_hex")
	keyText := r.URL.Query().Get("key")
	if keyHex != "" && keyText != "" {
		return nil, fmt.Errorf("use key_hex or key, not both")
	}
	if keyHex != "" {
		key, err := hex.DecodeString(keyHex)
		if err != nil {
			return nil, fmt.Errorf("key_hex must be hex")
		}
		if len(key) > 32 {
			return nil, fmt.Errorf("contract state key must be at most 32 bytes")
		}
		return key, nil
	}
	if len(keyText) > 32 {
		return nil, fmt.Errorf("contract state key must be at most 32 bytes")
	}
	return []byte(keyText), nil
}

func formatAccount(resp *frgpb.AccountResponse) accountResponse {
	return accountResponse{
		Pubkey:     hex.EncodeToString(resp.Pubkey),
		Balance:    resp.Balance,
		BalanceFRG: formatQuantaAsFRG(resp.Balance),
		Nonce:      resp.Nonce,
	}
}

func formatQuantaAsFRG(raw string) string {
	q, ok := new(big.Int).SetString(raw, 10)
	if !ok {
		return "0"
	}
	return denom.FormatFRG(q)
}

func formatContractState(resp *frgpb.ContractStateResponse) contractStateResponse {
	return contractStateResponse{
		ContractAddress: hex.EncodeToString(resp.ContractAddress),
		Exists:          resp.Exists,
		StateRoot:       hex.EncodeToString(resp.StateRoot),
		Key:             hex.EncodeToString(resp.Key),
		Found:           resp.Found,
		Value:           hex.EncodeToString(resp.Value),
	}
}

func requireMethod(w http.ResponseWriter, r *http.Request, method string) bool {
	if r.Method != method {
		writeError(w, http.StatusMethodNotAllowed, fmt.Errorf("%s only", method))
		return false
	}
	return true
}

func writeError(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, errorResponse{Error: err.Error()})
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}
