package proto

import (
	"encoding/json"

	"google.golang.org/grpc/encoding"
)

func init() {
	encoding.RegisterCodec(JSONCodec{})
	// Also register under "proto" so that standard gRPC clients (e.g. Python
	// grpcio) that send Content-Type: application/grpc+proto can communicate
	// with this server.  Our hand-written structs use JSON struct tags rather
	// than the protobuf binary wire format, so we use JSON for both codecs.
	encoding.RegisterCodec(protoNamedJSONCodec{})
}

// JSONCodec is a gRPC codec that uses encoding/json for marshaling and
// unmarshaling. It allows the hand-written proto structs (which carry JSON
// struct tags but do not implement proto.Message) to be used over a real
// gRPC transport between Go services.
//
// Usage:
//
//	// Client side – force JSON encoding for all RPCs on the connection.
//	conn, _ := grpc.NewClient(addr,
//	    grpc.WithDefaultCallOptions(grpc.ForceCodec(proto.JSONCodec{})),
//	    ...)
//
//	// Server side – the codec is auto-registered by the init() above; no
//	// extra configuration is needed.
type JSONCodec struct{}

// Name returns the content-subtype identifier used in the gRPC Content-Type
// header: "application/grpc+json".
func (JSONCodec) Name() string { return "json" }

// Marshal encodes v to JSON.
func (JSONCodec) Marshal(v interface{}) ([]byte, error) {
	return json.Marshal(v)
}

// Unmarshal decodes JSON data into v.
func (JSONCodec) Unmarshal(data []byte, v interface{}) error {
	return json.Unmarshal(data, v)
}

// protoNamedJSONCodec is identical to JSONCodec but registers itself as
// "proto".  This allows standard gRPC clients (Python grpcio, etc.) that
// negotiate Content-Type: application/grpc+proto to communicate with the
// server; the actual wire payload is JSON rather than protobuf binary.
type protoNamedJSONCodec struct{}

// Name returns "proto" so this codec is selected for the standard
// application/grpc+proto content type.
func (protoNamedJSONCodec) Name() string { return "proto" }

// Marshal encodes v to JSON.
func (protoNamedJSONCodec) Marshal(v interface{}) ([]byte, error) {
	return json.Marshal(v)
}

// Unmarshal decodes JSON data into v.
func (protoNamedJSONCodec) Unmarshal(data []byte, v interface{}) error {
	return json.Unmarshal(data, v)
}
