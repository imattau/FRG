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

type FRGClient interface {
	SubmitTx(ctx context.Context, in *RawBytes, opts ...grpc.CallOption) (*SubmitResponse, error)
	SubmitBatch(ctx context.Context, in *RawBytesArray, opts ...grpc.CallOption) (*SubmitResponse, error)
	SubscribeBlocks(ctx context.Context, in *Empty, opts ...grpc.CallOption) (FRG_SubscribeBlocksClient, error)
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

type FRGServer interface {
	SubmitTx(context.Context, *RawBytes) (*SubmitResponse, error)
	SubmitBatch(context.Context, *RawBytesArray) (*SubmitResponse, error)
	SubscribeBlocks(*Empty, FRG_SubscribeBlocksServer) error
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
