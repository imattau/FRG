package proto

import (
	"context"
	"encoding/json"
	"errors"

	"google.golang.org/grpc"
	"google.golang.org/grpc/encoding"
)

const codecName = "frg-json"

func init() {
	encoding.RegisterCodec(jsonCodec{})
}

type jsonCodec struct{}

func (jsonCodec) Name() string {
	return codecName
}

func (jsonCodec) Marshal(v any) ([]byte, error) {
	return json.Marshal(v)
}

func (jsonCodec) Unmarshal(data []byte, v any) error {
	return json.Unmarshal(data, v)
}

type RawBytes struct {
	Data []byte `json:"data,omitempty"`
}

type RawBytesArray struct {
	Data [][]byte `json:"data,omitempty"`
}

type SubmitResponse struct {
	Ok    bool   `json:"ok,omitempty"`
	Error string `json:"error,omitempty"`
}

type Empty struct{}

type StatusResponse struct {
	Height         uint64 `json:"height,omitempty"`
	StateRoot      []byte `json:"state_root,omitempty"`
	PeerCount      uint64 `json:"peer_count,omitempty"`
	MempoolLen     uint64 `json:"mempool_len,omitempty"`
	ValidatorCount uint64 `json:"validator_count,omitempty"`
	ConsensusRound uint32 `json:"consensus_round,omitempty"`
	ConsensusPhase string `json:"consensus_phase,omitempty"`
	GrpcOnly       bool   `json:"grpc_only,omitempty"`
}

type AccountRequest struct {
	Pubkey []byte `json:"pubkey,omitempty"`
}

type AccountResponse struct {
	Pubkey  []byte `json:"pubkey,omitempty"`
	Balance string `json:"balance,omitempty"`
	Nonce   uint64 `json:"nonce,omitempty"`
}

type ContractStateRequest struct {
	ContractAddress []byte `json:"contract_address,omitempty"`
	Key             []byte `json:"key,omitempty"`
}

type ContractStateResponse struct {
	ContractAddress []byte `json:"contract_address,omitempty"`
	Exists          bool   `json:"exists,omitempty"`
	StateRoot       []byte `json:"state_root,omitempty"`
	Key             []byte `json:"key,omitempty"`
	Found           bool   `json:"found,omitempty"`
	Value           []byte `json:"value,omitempty"`
}

type ValidatorEntry struct {
	Pubkey []byte `json:"pubkey,omitempty"`
	Bond   string `json:"bond,omitempty"`
}

type ValidatorList struct {
	Validators []*ValidatorEntry `json:"validators,omitempty"`
}

type MempoolEntry struct {
	Txid   []byte `json:"txid,omitempty"`
	Sender string `json:"sender,omitempty"`
	Nonce  uint64 `json:"nonce,omitempty"`
}

type MempoolList struct {
	Entries []*MempoolEntry `json:"entries,omitempty"`
}

type BlockTelemetryRequest struct {
	// Height zero means the latest committed block.
	Height uint64 `json:"height,omitempty"`
}

type TxTypeCount struct {
	Type  uint32 `json:"type,omitempty"`
	Name  string `json:"name,omitempty"`
	Count uint64 `json:"count,omitempty"`
}

type SignatureCount struct {
	Signature uint32 `json:"signature,omitempty"`
	Name      string `json:"name,omitempty"`
	Count     uint64 `json:"count,omitempty"`
}

type RGLevelTelemetry struct {
	Level             uint32            `json:"level,omitempty"`
	Scale             uint32            `json:"scale,omitempty"`
	NodeCount         uint64            `json:"node_count,omitempty"`
	SignatureCounts   []*SignatureCount `json:"signature_counts,omitempty"`
	ContractDensity   float64           `json:"contract_density,omitempty"`
	VolatileRegions   []uint64          `json:"volatile_regions,omitempty"`
	StagnantRegions   []uint64          `json:"stagnant_regions,omitempty"`
	TotalVolume       string            `json:"total_volume,omitempty"`
	Variance          string            `json:"variance,omitempty"`
	ContractTxCount   uint64            `json:"contract_tx_count,omitempty"`
	ContractNodeCount uint64            `json:"contract_node_count,omitempty"`
}

type BlockTelemetryResponse struct {
	Height                uint64              `json:"height,omitempty"`
	StateRoot             []byte              `json:"state_root,omitempty"`
	ProposerPubkey        []byte              `json:"proposer_pubkey,omitempty"`
	TxCount               uint64              `json:"tx_count,omitempty"`
	TotalValue            string              `json:"total_value,omitempty"`
	MeanValue             string              `json:"mean_value,omitempty"`
	Variance              string              `json:"variance,omitempty"`
	TxTypes               []*TxTypeCount      `json:"tx_types,omitempty"`
	Levels                []*RGLevelTelemetry `json:"levels,omitempty"`
	ContractStateIncluded bool                `json:"contract_state_included,omitempty"`
	Warning               string              `json:"warning,omitempty"`
}

type FRGClient interface {
	SubmitTx(ctx context.Context, in *RawBytes, opts ...grpc.CallOption) (*SubmitResponse, error)
	SubmitBatch(ctx context.Context, in *RawBytesArray, opts ...grpc.CallOption) (*SubmitResponse, error)
	SubscribeBlocks(ctx context.Context, in *Empty, opts ...grpc.CallOption) (FRG_SubscribeBlocksClient, error)
	GetStatus(ctx context.Context, in *Empty, opts ...grpc.CallOption) (*StatusResponse, error)
	GetAccount(ctx context.Context, in *AccountRequest, opts ...grpc.CallOption) (*AccountResponse, error)
	GetContractState(ctx context.Context, in *ContractStateRequest, opts ...grpc.CallOption) (*ContractStateResponse, error)
	ListValidators(ctx context.Context, in *Empty, opts ...grpc.CallOption) (*ValidatorList, error)
	ListMempool(ctx context.Context, in *Empty, opts ...grpc.CallOption) (*MempoolList, error)
	GetBlockTelemetry(ctx context.Context, in *BlockTelemetryRequest, opts ...grpc.CallOption) (*BlockTelemetryResponse, error)
}

type fRGClient struct {
	cc grpc.ClientConnInterface
}

func NewFRGClient(cc grpc.ClientConnInterface) FRGClient {
	return &fRGClient{cc: cc}
}

func (c *fRGClient) SubmitTx(ctx context.Context, in *RawBytes, opts ...grpc.CallOption) (*SubmitResponse, error) {
	out := new(SubmitResponse)
	err := c.cc.Invoke(ctx, "/frg.FRG/SubmitTx", in, out, opts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *fRGClient) SubmitBatch(ctx context.Context, in *RawBytesArray, opts ...grpc.CallOption) (*SubmitResponse, error) {
	out := new(SubmitResponse)
	err := c.cc.Invoke(ctx, "/frg.FRG/SubmitBatch", in, out, opts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *fRGClient) SubscribeBlocks(ctx context.Context, in *Empty, opts ...grpc.CallOption) (FRG_SubscribeBlocksClient, error) {
	stream, err := c.cc.NewStream(ctx, &FRG_ServiceDesc.Streams[0], "/frg.FRG/SubscribeBlocks", opts...)
	if err != nil {
		return nil, err
	}
	x := &fRGSubscribeBlocksClient{stream}
	if err := x.ClientStream.SendMsg(in); err != nil {
		return nil, err
	}
	if err := x.ClientStream.CloseSend(); err != nil {
		return nil, err
	}
	return x, nil
}

type FRG_SubscribeBlocksClient interface {
	Recv() (*RawBytes, error)
	grpc.ClientStream
}

type fRGSubscribeBlocksClient struct {
	grpc.ClientStream
}

func (x *fRGSubscribeBlocksClient) Recv() (*RawBytes, error) {
	m := new(RawBytes)
	if err := x.ClientStream.RecvMsg(m); err != nil {
		return nil, err
	}
	return m, nil
}

func (c *fRGClient) GetStatus(ctx context.Context, in *Empty, opts ...grpc.CallOption) (*StatusResponse, error) {
	out := new(StatusResponse)
	err := c.cc.Invoke(ctx, "/frg.FRG/GetStatus", in, out, opts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *fRGClient) GetAccount(ctx context.Context, in *AccountRequest, opts ...grpc.CallOption) (*AccountResponse, error) {
	out := new(AccountResponse)
	err := c.cc.Invoke(ctx, "/frg.FRG/GetAccount", in, out, opts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *fRGClient) GetContractState(ctx context.Context, in *ContractStateRequest, opts ...grpc.CallOption) (*ContractStateResponse, error) {
	out := new(ContractStateResponse)
	err := c.cc.Invoke(ctx, "/frg.FRG/GetContractState", in, out, opts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *fRGClient) ListValidators(ctx context.Context, in *Empty, opts ...grpc.CallOption) (*ValidatorList, error) {
	out := new(ValidatorList)
	err := c.cc.Invoke(ctx, "/frg.FRG/ListValidators", in, out, opts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *fRGClient) ListMempool(ctx context.Context, in *Empty, opts ...grpc.CallOption) (*MempoolList, error) {
	out := new(MempoolList)
	err := c.cc.Invoke(ctx, "/frg.FRG/ListMempool", in, out, opts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *fRGClient) GetBlockTelemetry(ctx context.Context, in *BlockTelemetryRequest, opts ...grpc.CallOption) (*BlockTelemetryResponse, error) {
	out := new(BlockTelemetryResponse)
	err := c.cc.Invoke(ctx, "/frg.FRG/GetBlockTelemetry", in, out, opts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

type FRGServer interface {
	SubmitTx(context.Context, *RawBytes) (*SubmitResponse, error)
	SubmitBatch(context.Context, *RawBytesArray) (*SubmitResponse, error)
	SubscribeBlocks(*Empty, FRG_SubscribeBlocksServer) error
	GetStatus(context.Context, *Empty) (*StatusResponse, error)
	GetAccount(context.Context, *AccountRequest) (*AccountResponse, error)
	GetContractState(context.Context, *ContractStateRequest) (*ContractStateResponse, error)
	ListValidators(context.Context, *Empty) (*ValidatorList, error)
	ListMempool(context.Context, *Empty) (*MempoolList, error)
	GetBlockTelemetry(context.Context, *BlockTelemetryRequest) (*BlockTelemetryResponse, error)
	mustEmbedUnimplementedFRGServer()
}

type UnimplementedFRGServer struct{}

func (UnimplementedFRGServer) SubmitTx(context.Context, *RawBytes) (*SubmitResponse, error) {
	return nil, errors.New("method SubmitTx not implemented")
}

func (UnimplementedFRGServer) SubmitBatch(context.Context, *RawBytesArray) (*SubmitResponse, error) {
	return nil, errors.New("method SubmitBatch not implemented")
}

func (UnimplementedFRGServer) SubscribeBlocks(*Empty, FRG_SubscribeBlocksServer) error {
	return errors.New("method SubscribeBlocks not implemented")
}

func (UnimplementedFRGServer) GetStatus(context.Context, *Empty) (*StatusResponse, error) {
	return nil, errors.New("method GetStatus not implemented")
}

func (UnimplementedFRGServer) GetAccount(context.Context, *AccountRequest) (*AccountResponse, error) {
	return nil, errors.New("method GetAccount not implemented")
}

func (UnimplementedFRGServer) GetContractState(context.Context, *ContractStateRequest) (*ContractStateResponse, error) {
	return nil, errors.New("method GetContractState not implemented")
}

func (UnimplementedFRGServer) ListValidators(context.Context, *Empty) (*ValidatorList, error) {
	return nil, errors.New("method ListValidators not implemented")
}

func (UnimplementedFRGServer) ListMempool(context.Context, *Empty) (*MempoolList, error) {
	return nil, errors.New("method ListMempool not implemented")
}

func (UnimplementedFRGServer) GetBlockTelemetry(context.Context, *BlockTelemetryRequest) (*BlockTelemetryResponse, error) {
	return nil, errors.New("method GetBlockTelemetry not implemented")
}

func (UnimplementedFRGServer) mustEmbedUnimplementedFRGServer() {}

type UnsafeFRGServer interface {
	mustEmbedUnimplementedFRGServer()
}

type FRG_SubscribeBlocksServer interface {
	Send(*RawBytes) error
	grpc.ServerStream
}

func RegisterFRGServer(s grpc.ServiceRegistrar, srv FRGServer) {
	s.RegisterService(&FRG_ServiceDesc, srv)
}

func _FRG_SubmitTx_Handler(srv any, ctx context.Context, dec func(any) error, interceptor grpc.UnaryServerInterceptor) (any, error) {
	in := new(RawBytes)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(FRGServer).SubmitTx(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: "/frg.FRG/SubmitTx",
	}
	handler := func(ctx context.Context, req any) (any, error) {
		return srv.(FRGServer).SubmitTx(ctx, req.(*RawBytes))
	}
	return interceptor(ctx, in, info, handler)
}

func _FRG_SubmitBatch_Handler(srv any, ctx context.Context, dec func(any) error, interceptor grpc.UnaryServerInterceptor) (any, error) {
	in := new(RawBytesArray)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(FRGServer).SubmitBatch(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: "/frg.FRG/SubmitBatch",
	}
	handler := func(ctx context.Context, req any) (any, error) {
		return srv.(FRGServer).SubmitBatch(ctx, req.(*RawBytesArray))
	}
	return interceptor(ctx, in, info, handler)
}

func _FRG_SubscribeBlocks_Handler(srv any, stream grpc.ServerStream) error {
	m := new(Empty)
	if err := stream.RecvMsg(m); err != nil {
		return err
	}
	return srv.(FRGServer).SubscribeBlocks(m, &fRGSubscribeBlocksServer{stream})
}

func _FRG_GetStatus_Handler(srv any, ctx context.Context, dec func(any) error, interceptor grpc.UnaryServerInterceptor) (any, error) {
	in := new(Empty)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(FRGServer).GetStatus(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: "/frg.FRG/GetStatus",
	}
	handler := func(ctx context.Context, req any) (any, error) {
		return srv.(FRGServer).GetStatus(ctx, req.(*Empty))
	}
	return interceptor(ctx, in, info, handler)
}

func _FRG_GetAccount_Handler(srv any, ctx context.Context, dec func(any) error, interceptor grpc.UnaryServerInterceptor) (any, error) {
	in := new(AccountRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(FRGServer).GetAccount(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: "/frg.FRG/GetAccount",
	}
	handler := func(ctx context.Context, req any) (any, error) {
		return srv.(FRGServer).GetAccount(ctx, req.(*AccountRequest))
	}
	return interceptor(ctx, in, info, handler)
}

func _FRG_GetContractState_Handler(srv any, ctx context.Context, dec func(any) error, interceptor grpc.UnaryServerInterceptor) (any, error) {
	in := new(ContractStateRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(FRGServer).GetContractState(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: "/frg.FRG/GetContractState",
	}
	handler := func(ctx context.Context, req any) (any, error) {
		return srv.(FRGServer).GetContractState(ctx, req.(*ContractStateRequest))
	}
	return interceptor(ctx, in, info, handler)
}

func _FRG_ListValidators_Handler(srv any, ctx context.Context, dec func(any) error, interceptor grpc.UnaryServerInterceptor) (any, error) {
	in := new(Empty)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(FRGServer).ListValidators(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: "/frg.FRG/ListValidators",
	}
	handler := func(ctx context.Context, req any) (any, error) {
		return srv.(FRGServer).ListValidators(ctx, req.(*Empty))
	}
	return interceptor(ctx, in, info, handler)
}

func _FRG_ListMempool_Handler(srv any, ctx context.Context, dec func(any) error, interceptor grpc.UnaryServerInterceptor) (any, error) {
	in := new(Empty)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(FRGServer).ListMempool(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: "/frg.FRG/ListMempool",
	}
	handler := func(ctx context.Context, req any) (any, error) {
		return srv.(FRGServer).ListMempool(ctx, req.(*Empty))
	}
	return interceptor(ctx, in, info, handler)
}

func _FRG_GetBlockTelemetry_Handler(srv any, ctx context.Context, dec func(any) error, interceptor grpc.UnaryServerInterceptor) (any, error) {
	in := new(BlockTelemetryRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(FRGServer).GetBlockTelemetry(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: "/frg.FRG/GetBlockTelemetry",
	}
	handler := func(ctx context.Context, req any) (any, error) {
		return srv.(FRGServer).GetBlockTelemetry(ctx, req.(*BlockTelemetryRequest))
	}
	return interceptor(ctx, in, info, handler)
}

type fRGSubscribeBlocksServer struct {
	grpc.ServerStream
}

func (x *fRGSubscribeBlocksServer) Send(m *RawBytes) error {
	return x.ServerStream.SendMsg(m)
}

var FRG_ServiceDesc = grpc.ServiceDesc{
	ServiceName: "frg.FRG",
	HandlerType: (*FRGServer)(nil),
	Methods: []grpc.MethodDesc{
		{
			MethodName: "SubmitTx",
			Handler:    _FRG_SubmitTx_Handler,
		},
		{
			MethodName: "SubmitBatch",
			Handler:    _FRG_SubmitBatch_Handler,
		},
		{
			MethodName: "GetStatus",
			Handler:    _FRG_GetStatus_Handler,
		},
		{
			MethodName: "GetAccount",
			Handler:    _FRG_GetAccount_Handler,
		},
		{
			MethodName: "GetContractState",
			Handler:    _FRG_GetContractState_Handler,
		},
		{
			MethodName: "ListValidators",
			Handler:    _FRG_ListValidators_Handler,
		},
		{
			MethodName: "ListMempool",
			Handler:    _FRG_ListMempool_Handler,
		},
		{
			MethodName: "GetBlockTelemetry",
			Handler:    _FRG_GetBlockTelemetry_Handler,
		},
	},
	Streams: []grpc.StreamDesc{
		{
			StreamName:    "SubscribeBlocks",
			Handler:       _FRG_SubscribeBlocks_Handler,
			ServerStreams: true,
		},
	},
	Metadata: "proto/frg.proto",
}
