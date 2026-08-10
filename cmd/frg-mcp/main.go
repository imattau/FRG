package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"math/big"
	"net/textproto"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/imattau/frg/core/tx"
	frgpb "github.com/imattau/frg/proto"
	"github.com/imattau/frg/wallet"
)

const protocolVersion = "2024-11-05"

type policy struct {
	AllowSubmit       bool     `json:"allow_submit"`
	AllowDeploy       bool     `json:"allow_deploy"`
	AllowBond         bool     `json:"allow_bond"`
	MaxTransfer       string   `json:"max_transfer"`
	DailyLimit        string   `json:"daily_limit"`
	AllowedRecipients []string `json:"allowed_recipients"`
	AllowedContracts  []string `json:"allowed_contracts"`

	maxTransfer       *big.Int
	dailyLimit        *big.Int
	allowedRecipients map[string]struct{}
	allowedContracts  map[string]struct{}
	spentToday        *big.Int
}

type mcpServer struct {
	w         *wallet.Wallet
	faucetURL string
	policy    *policy
}

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type tool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
}

type toolCallParams struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments,omitempty"`
}

type toolResult struct {
	Content []toolContent `json:"content"`
	IsError bool          `json:"isError,omitempty"`
}

type toolContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

func main() {
	nodeAddr := flag.String("node", "127.0.0.1:50051", "FRG node gRPC address")
	keyPath := flag.String("key", "frg-mcp.key", "agent wallet seed/private key file")
	chainID := flag.String("chain-id", tx.DefaultChainID, "chain ID for transaction signatures")
	createKey := flag.Bool("create-key", false, "create --key if it does not exist")
	policyPath := flag.String("policy", "", "optional JSON policy file for autonomous spending")
	autonomous := flag.Bool("autonomous", false, "enable submit/call tools with default zero-value spending limit unless policy raises it")
	faucetURL := flag.String("faucet-url", "", "optional faucet URL for frg_request_faucet")
	flag.Parse()

	log.SetOutput(os.Stderr)

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
		log.Printf("created MCP wallet key at %s", *keyPath)
	}

	pol, err := loadPolicy(*policyPath, *autonomous)
	if err != nil {
		log.Fatalf("load policy: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client, err := wallet.Dial(ctx, *nodeAddr, kp, *chainID)
	if err != nil {
		log.Fatalf("dial node: %v", err)
	}
	defer client.Close()

	s := &mcpServer{w: client.Wallet, faucetURL: *faucetURL, policy: pol}
	if err := s.serve(os.Stdin, os.Stdout); err != nil && err != io.EOF {
		log.Fatalf("serve MCP: %v", err)
	}
}

func loadPolicy(path string, autonomous bool) (*policy, error) {
	p := &policy{
		AllowSubmit: autonomous,
		maxTransfer: big.NewInt(0),
		dailyLimit:  big.NewInt(0),
		spentToday:  big.NewInt(0),
	}
	if path != "" {
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		if err := json.Unmarshal(data, p); err != nil {
			return nil, err
		}
	}
	var err error
	p.maxTransfer, err = parsePolicyAmount(p.MaxTransfer)
	if err != nil {
		return nil, fmt.Errorf("max_transfer: %w", err)
	}
	p.dailyLimit, err = parsePolicyAmount(p.DailyLimit)
	if err != nil {
		return nil, fmt.Errorf("daily_limit: %w", err)
	}
	p.allowedRecipients = make(map[string]struct{}, len(p.AllowedRecipients))
	for _, recipient := range p.AllowedRecipients {
		p.allowedRecipients[strings.ToLower(recipient)] = struct{}{}
	}
	p.allowedContracts = make(map[string]struct{}, len(p.AllowedContracts))
	for _, contractAddr := range p.AllowedContracts {
		p.allowedContracts[strings.ToLower(contractAddr)] = struct{}{}
	}
	if p.spentToday == nil {
		p.spentToday = big.NewInt(0)
	}
	return p, nil
}

func parsePolicyAmount(raw string) (*big.Int, error) {
	if raw == "" {
		return big.NewInt(0), nil
	}
	amount, ok := new(big.Int).SetString(raw, 10)
	if !ok || amount.Sign() < 0 {
		return nil, fmt.Errorf("must be a non-negative base-10 integer")
	}
	return amount, nil
}

func (s *mcpServer) serve(r io.Reader, w io.Writer) error {
	br := bufio.NewReader(r)
	for {
		msg, err := readMessage(br)
		if err != nil {
			return err
		}
		var req rpcRequest
		if err := json.Unmarshal(msg, &req); err != nil {
			if err := writeMessage(w, rpcResponse{JSONRPC: "2.0", Error: &rpcError{Code: -32700, Message: err.Error()}}); err != nil {
				return err
			}
			continue
		}
		if len(req.ID) == 0 {
			continue
		}
		resp := s.handle(context.Background(), req)
		if err := writeMessage(w, resp); err != nil {
			return err
		}
	}
}

func readMessage(r *bufio.Reader) ([]byte, error) {
	tp := textproto.NewReader(r)
	headers, err := tp.ReadMIMEHeader()
	if err != nil {
		return nil, err
	}
	rawLen := headers.Get("Content-Length")
	if rawLen == "" {
		return nil, fmt.Errorf("missing Content-Length")
	}
	n, err := strconv.Atoi(rawLen)
	if err != nil || n < 0 {
		return nil, fmt.Errorf("invalid Content-Length")
	}
	buf := make([]byte, n)
	if _, err := io.ReadFull(r, buf); err != nil {
		return nil, err
	}
	return buf, nil
}

func writeMessage(w io.Writer, resp rpcResponse) error {
	data, err := json.Marshal(resp)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "Content-Length: %d\r\n\r\n", len(data)); err != nil {
		return err
	}
	_, err = w.Write(data)
	return err
}

func (s *mcpServer) handle(ctx context.Context, req rpcRequest) rpcResponse {
	resp := rpcResponse{JSONRPC: "2.0", ID: req.ID}
	switch req.Method {
	case "initialize":
		resp.Result = map[string]any{
			"protocolVersion": protocolVersion,
			"capabilities": map[string]any{
				"tools": map[string]any{"listChanged": false},
			},
			"serverInfo": map[string]any{"name": "frg-mcp", "version": "0.1.0"},
		}
	case "tools/list":
		resp.Result = map[string]any{"tools": s.tools()}
	case "tools/call":
		var params toolCallParams
		if err := json.Unmarshal(req.Params, &params); err != nil {
			resp.Error = &rpcError{Code: -32602, Message: err.Error()}
			return resp
		}
		result, err := s.callTool(ctx, params.Name, params.Arguments)
		if err != nil {
			resp.Result = errorToolResult(err)
			return resp
		}
		resp.Result = result
	default:
		resp.Error = &rpcError{Code: -32601, Message: "method not found"}
	}
	return resp
}

func (s *mcpServer) tools() []tool {
	return []tool{
		readTool("frg_get_pubkey", "Return this agent wallet public key and chain ID.", objectSchema(nil, nil)),
		readTool("frg_get_status", "Return FRG node status.", objectSchema(nil, nil)),
		readTool("frg_get_account", "Return account balance and nonce. Defaults to this agent wallet.", objectSchema(map[string]any{"pubkey": stringSchema("32-byte public key hex")}, nil)),
		readTool("frg_list_validators", "List active bonded validators.", objectSchema(nil, nil)),
		readTool("frg_get_contract_state", "Query contract existence, state root, and optionally one state key.", objectSchema(map[string]any{
			"contract_address": stringSchema("32-byte contract address hex"),
			"key":              stringSchema("optional text key, max 32 bytes"),
			"key_hex":          stringSchema("optional raw key hex, max 32 bytes"),
		}, []string{"contract_address"})),
		readTool("frg_predict_contract_address", "Predict this wallet's contract address for a nonce, defaulting to next nonce.", objectSchema(map[string]any{"nonce": stringSchema("optional deploy nonce")}, nil)),
		writeTool("frg_transfer", "Autonomously send FRG if policy allows it.", objectSchema(map[string]any{"to": stringSchema("recipient pubkey hex"), "amount": stringSchema("base-10 quanta")}, []string{"to", "amount"})),
		writeTool("frg_bond", "Autonomously bond this wallet as a validator if policy allows it.", objectSchema(map[string]any{"amount": stringSchema("base-10 quanta")}, []string{"amount"})),
		writeTool("frg_contract_deploy", "Autonomously deploy a WASM contract if policy allows it.", objectSchema(map[string]any{"wasm_hex": stringSchema("WASM bytes as hex"), "value": stringSchema("optional endowment")}, []string{"wasm_hex"})),
		writeTool("frg_contract_call", "Autonomously call a contract if policy allows it.", objectSchema(map[string]any{
			"contract_address": stringSchema("32-byte contract address hex"),
			"function":         stringSchema("optional 4-byte exported function name"),
			"call_data_hex":    stringSchema("optional raw calldata hex"),
			"value":            stringSchema("optional FRG value"),
		}, []string{"contract_address"})),
		readTool("frg_request_faucet", "Request faucet funds for this wallet or a supplied pubkey, if --faucet-url is configured.", objectSchema(map[string]any{"pubkey": stringSchema("optional 32-byte pubkey hex")}, nil)),
	}
}

func readTool(name, description string, schema map[string]any) tool {
	return tool{Name: name, Description: description, InputSchema: schema}
}

func writeTool(name, description string, schema map[string]any) tool {
	return tool{Name: name, Description: description, InputSchema: schema}
}

func objectSchema(properties map[string]any, required []string) map[string]any {
	if properties == nil {
		properties = map[string]any{}
	}
	s := map[string]any{"type": "object", "properties": properties, "additionalProperties": false}
	if len(required) > 0 {
		s["required"] = required
	}
	return s
}

func stringSchema(description string) map[string]any {
	return map[string]any{"type": "string", "description": description}
}

func (s *mcpServer) callTool(ctx context.Context, name string, args json.RawMessage) (*toolResult, error) {
	switch name {
	case "frg_get_pubkey":
		return jsonTool(map[string]string{"pubkey": s.w.PublicKeyHex(), "chain_id": s.w.ChainID()})
	case "frg_get_status":
		resp, err := s.w.Status(ctx)
		if err != nil {
			return nil, err
		}
		return jsonTool(resp)
	case "frg_get_account":
		var in struct {
			Pubkey string `json:"pubkey"`
		}
		if err := decodeArgs(args, &in); err != nil {
			return nil, err
		}
		pubkey := s.w.PublicKey()
		if in.Pubkey != "" {
			var err error
			pubkey, err = wallet.DecodePubKey(in.Pubkey)
			if err != nil {
				return nil, err
			}
		}
		resp, err := s.w.Account(ctx, pubkey)
		if err != nil {
			return nil, err
		}
		return jsonTool(formatAccount(resp))
	case "frg_list_validators":
		resp, err := s.w.Validators(ctx)
		if err != nil {
			return nil, err
		}
		return jsonTool(formatValidators(resp))
	case "frg_get_contract_state":
		var in struct {
			ContractAddress string `json:"contract_address"`
			Key             string `json:"key"`
			KeyHex          string `json:"key_hex"`
		}
		if err := decodeArgs(args, &in); err != nil {
			return nil, err
		}
		addr, err := wallet.DecodePubKey(in.ContractAddress)
		if err != nil {
			return nil, fmt.Errorf("contract_address must be 32-byte hex")
		}
		key, err := decodeKey(in.Key, in.KeyHex)
		if err != nil {
			return nil, err
		}
		resp, err := s.w.ContractState(ctx, addr, key)
		if err != nil {
			return nil, err
		}
		return jsonTool(formatContractState(resp))
	case "frg_predict_contract_address":
		var in struct {
			Nonce string `json:"nonce"`
		}
		if err := decodeArgs(args, &in); err != nil {
			return nil, err
		}
		nonce, err := s.resolveNonce(ctx, in.Nonce)
		if err != nil {
			return nil, err
		}
		addr := s.w.ContractAddress(nonce)
		return jsonTool(map[string]any{"nonce": nonce, "contract_address": hex.EncodeToString(addr[:])})
	case "frg_transfer":
		var in struct {
			To     string `json:"to"`
			Amount string `json:"amount"`
		}
		if err := decodeArgs(args, &in); err != nil {
			return nil, err
		}
		to, err := wallet.DecodePubKey(in.To)
		if err != nil {
			return nil, err
		}
		amount, err := parsePositiveAmount(in.Amount)
		if err != nil {
			return nil, err
		}
		if err := s.policy.allowSpend("transfer", in.To, amount, false, false); err != nil {
			return nil, err
		}
		resp, err := s.w.Transfer(ctx, to, amount)
		if err != nil {
			return nil, err
		}
		s.policy.recordSpend(amount)
		return jsonTool(resp)
	case "frg_bond":
		var in struct {
			Amount string `json:"amount"`
		}
		if err := decodeArgs(args, &in); err != nil {
			return nil, err
		}
		amount, err := parsePositiveAmount(in.Amount)
		if err != nil {
			return nil, err
		}
		if err := s.policy.allowSpend("bond", "", amount, true, false); err != nil {
			return nil, err
		}
		resp, err := s.w.Bond(ctx, amount)
		if err != nil {
			return nil, err
		}
		s.policy.recordSpend(amount)
		return jsonTool(resp)
	case "frg_contract_deploy":
		var in struct {
			WasmHex string `json:"wasm_hex"`
			Value   string `json:"value"`
		}
		if err := decodeArgs(args, &in); err != nil {
			return nil, err
		}
		wasm, err := hex.DecodeString(in.WasmHex)
		if err != nil || len(wasm) == 0 {
			return nil, fmt.Errorf("wasm_hex must be non-empty hex")
		}
		value, err := parseOptionalAmount(in.Value)
		if err != nil {
			return nil, err
		}
		if err := s.policy.allowSpend("contract_deploy", "", value, false, true); err != nil {
			return nil, err
		}
		resp, err := s.w.DeployContract(ctx, wasm, value)
		if err != nil {
			return nil, err
		}
		s.policy.recordSpend(value)
		return jsonTool(resp)
	case "frg_contract_call":
		var in struct {
			ContractAddress string `json:"contract_address"`
			Function        string `json:"function"`
			CallDataHex     string `json:"call_data_hex"`
			Value           string `json:"value"`
		}
		if err := decodeArgs(args, &in); err != nil {
			return nil, err
		}
		addr, err := wallet.DecodePubKey(in.ContractAddress)
		if err != nil {
			return nil, fmt.Errorf("contract_address must be 32-byte hex")
		}
		callData, err := contractCallData(in.Function, in.CallDataHex)
		if err != nil {
			return nil, err
		}
		value, err := parseOptionalAmount(in.Value)
		if err != nil {
			return nil, err
		}
		if err := s.policy.allowSpend("contract_call", in.ContractAddress, value, false, false); err != nil {
			return nil, err
		}
		resp, err := s.w.CallContract(ctx, addr, callData, value)
		if err != nil {
			return nil, err
		}
		s.policy.recordSpend(value)
		return jsonTool(resp)
	case "frg_request_faucet":
		if s.faucetURL == "" {
			return nil, fmt.Errorf("faucet URL is not configured")
		}
		var in struct {
			Pubkey string `json:"pubkey"`
		}
		if err := decodeArgs(args, &in); err != nil {
			return nil, err
		}
		pubkey := s.w.PublicKey()
		if in.Pubkey != "" {
			var err error
			pubkey, err = wallet.DecodePubKey(in.Pubkey)
			if err != nil {
				return nil, err
			}
		}
		resp, err := wallet.RequestFaucet(ctx, s.faucetURL, pubkey)
		if err != nil {
			return nil, err
		}
		return jsonTool(resp)
	default:
		return nil, fmt.Errorf("unknown tool %q", name)
	}
}

func decodeArgs(raw json.RawMessage, out any) error {
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		raw = []byte("{}")
	}
	return json.Unmarshal(raw, out)
}

func jsonTool(v any) (*toolResult, error) {
	data, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return &toolResult{Content: []toolContent{{Type: "text", Text: string(data)}}}, nil
}

func errorToolResult(err error) *toolResult {
	return &toolResult{IsError: true, Content: []toolContent{{Type: "text", Text: err.Error()}}}
}

func parsePositiveAmount(raw string) (*big.Int, error) {
	if raw == "" {
		return nil, fmt.Errorf("amount is required")
	}
	amount, ok := new(big.Int).SetString(raw, 10)
	if !ok || amount.Sign() <= 0 {
		return nil, fmt.Errorf("amount must be a positive base-10 integer")
	}
	return amount, nil
}

func parseOptionalAmount(raw string) (*big.Int, error) {
	if raw == "" {
		return big.NewInt(0), nil
	}
	amount, ok := new(big.Int).SetString(raw, 10)
	if !ok || amount.Sign() < 0 {
		return nil, fmt.Errorf("value must be a non-negative base-10 integer")
	}
	return amount, nil
}

func decodeKey(keyText, keyHex string) ([]byte, error) {
	if keyText != "" && keyHex != "" {
		return nil, fmt.Errorf("use key or key_hex, not both")
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

func contractCallData(function, callDataHex string) ([]byte, error) {
	if callDataHex != "" {
		data, err := hex.DecodeString(callDataHex)
		if err != nil {
			return nil, fmt.Errorf("call_data_hex must be hex")
		}
		return data, nil
	}
	if function == "" {
		return []byte("call"), nil
	}
	if len(function) != 4 {
		return nil, fmt.Errorf("function must be exactly 4 bytes")
	}
	return []byte(function), nil
}

func (s *mcpServer) resolveNonce(ctx context.Context, raw string) (uint64, error) {
	if raw != "" {
		nonce, ok := new(big.Int).SetString(raw, 10)
		if !ok || nonce.Sign() <= 0 || !nonce.IsUint64() {
			return 0, fmt.Errorf("nonce must be a positive uint64")
		}
		return nonce.Uint64(), nil
	}
	acct, err := s.w.OwnAccount(ctx)
	if err != nil {
		return 0, err
	}
	return acct.Nonce + 1, nil
}

func (p *policy) allowSpend(action string, target string, amount *big.Int, bond bool, deploy bool) error {
	if p == nil || !p.AllowSubmit {
		return fmt.Errorf("%s denied: allow_submit is false", action)
	}
	if bond && !p.AllowBond {
		return fmt.Errorf("%s denied: allow_bond is false", action)
	}
	if deploy && !p.AllowDeploy {
		return fmt.Errorf("%s denied: allow_deploy is false", action)
	}
	if amount == nil {
		amount = big.NewInt(0)
	}
	if amount.Sign() > 0 && p.maxTransfer.Sign() == 0 {
		return fmt.Errorf("%s denied: max_transfer is zero", action)
	}
	if p.maxTransfer.Sign() > 0 && amount.Cmp(p.maxTransfer) > 0 {
		return fmt.Errorf("%s denied: amount exceeds max_transfer", action)
	}
	nextSpent := new(big.Int).Add(p.spentToday, amount)
	if amount.Sign() > 0 && p.dailyLimit.Sign() == 0 {
		return fmt.Errorf("%s denied: daily_limit is zero", action)
	}
	if p.dailyLimit.Sign() > 0 && nextSpent.Cmp(p.dailyLimit) > 0 {
		return fmt.Errorf("%s denied: amount exceeds daily_limit", action)
	}
	target = strings.ToLower(target)
	if action == "transfer" && len(p.allowedRecipients) > 0 {
		if _, ok := p.allowedRecipients[target]; !ok {
			return fmt.Errorf("transfer denied: recipient is not allowed")
		}
	}
	if action == "contract_call" && len(p.allowedContracts) > 0 {
		if _, ok := p.allowedContracts[target]; !ok {
			return fmt.Errorf("contract_call denied: contract is not allowed")
		}
	}
	return nil
}

func (p *policy) recordSpend(amount *big.Int) {
	if p == nil || amount == nil || amount.Sign() <= 0 {
		return
	}
	p.spentToday.Add(p.spentToday, amount)
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

type contractStateResponse struct {
	ContractAddress string `json:"contract_address"`
	Exists          bool   `json:"exists"`
	StateRoot       string `json:"state_root,omitempty"`
	Key             string `json:"key,omitempty"`
	Found           bool   `json:"found"`
	Value           string `json:"value,omitempty"`
}

func formatAccount(resp *frgpb.AccountResponse) accountResponse {
	return accountResponse{
		Pubkey:  hex.EncodeToString(resp.Pubkey),
		Balance: resp.Balance,
		Nonce:   resp.Nonce,
	}
}

func formatValidators(resp *frgpb.ValidatorList) validatorsResponse {
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
	return out
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
