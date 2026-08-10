package main

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"math/big"
	"net/http"
	"os"
	"time"

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
	Pubkey  string `json:"pubkey"`
	Balance string `json:"balance"`
	Nonce   uint64 `json:"nonce"`
}

type validatorEntry struct {
	Pubkey string `json:"pubkey"`
	Bond   string `json:"bond"`
}

type validatorsResponse struct {
	Validators []validatorEntry `json:"validators"`
}

type transferRequest struct {
	To     string `json:"to"`
	Amount string `json:"amount"`
}

type bondRequest struct {
	Amount string `json:"amount"`
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
		if !*createKey || !os.IsNotExist(err) {
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
			Pubkey: hex.EncodeToString(v.Pubkey),
			Bond:   v.Bond,
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
	amount, err := parseAmount(req.Amount)
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
	amount, err := parseAmount(req.Amount)
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

func parseAmount(raw string) (*big.Int, error) {
	if raw == "" {
		return nil, fmt.Errorf("amount is required")
	}
	amount, ok := new(big.Int).SetString(raw, 10)
	if !ok || amount.Sign() <= 0 {
		return nil, fmt.Errorf("amount must be a positive base-10 integer")
	}
	return amount, nil
}

func formatAccount(resp *frgpb.AccountResponse) accountResponse {
	return accountResponse{
		Pubkey:  hex.EncodeToString(resp.Pubkey),
		Balance: resp.Balance,
		Nonce:   resp.Nonce,
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
