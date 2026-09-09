package worker

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"

	pb "github.com/azure/azure-functions-golang-worker/worker/proto"
)

// decodeGenericInput is the explicit raw-or-JSON contract for generic bindings.
// Legacy trigger models retain their metadata-field matching in convertToTypeValue.
// Both are used by the same argument binder and invocation pipeline.
func decodeGenericInput(t reflect.Type, data *pb.TypedData) (value reflect.Value, err error) {
	// JSON unmarshaling can execute application code before the handler's panic
	// boundary. Keep a custom unmarshaler panic local to this invocation.
	defer func() {
		if recovered := recover(); recovered != nil {
			value = reflect.Value{}
			err = fmt.Errorf("decode %s: %v", t, recovered)
		}
	}()
	if data == nil || data.Data == nil {
		return reflect.Value{}, fmt.Errorf("missing payload")
	}
	if t.Kind() == reflect.Ptr {
		base := t.Elem()
		for base.Kind() == reflect.Ptr {
			base = base.Elem()
		}
		rawTarget := base.Kind() == reflect.String || (base.Kind() == reflect.Slice && base.Elem().Kind() == reflect.Uint8)
		var raw []byte
		switch value := data.Data.(type) {
		case *pb.TypedData_String_:
			raw = []byte(value.String_)
		case *pb.TypedData_Json:
			raw = []byte(value.Json)
		case *pb.TypedData_Bytes:
			raw = value.Bytes
		}
		if !rawTarget && bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			return reflect.Zero(t), nil
		}
		value, err := decodeGenericInput(t.Elem(), data)
		if err != nil {
			return reflect.Value{}, err
		}
		ptr := reflect.New(t.Elem())
		ptr.Elem().Set(value)
		return ptr.Convert(t), nil
	}
	if values, ok := typedDataCollectionValues(data); ok {
		if t.Kind() != reflect.Slice || t.Elem().Kind() == reflect.Uint8 {
			return reflect.Value{}, fmt.Errorf("host collection requires a slice, got %s", t)
		}
		result := reflect.MakeSlice(t, len(values), len(values))
		for i, item := range values {
			value, err := decodeGenericInput(t.Elem(), item)
			if err != nil {
				return reflect.Value{}, fmt.Errorf("element %d: %w", i, err)
			}
			result.Index(i).Set(value)
		}
		return result, nil
	}
	var raw []byte
	switch value := data.Data.(type) {
	case *pb.TypedData_String_:
		raw = []byte(value.String_)
	case *pb.TypedData_Json:
		raw = []byte(value.Json)
	case *pb.TypedData_Bytes:
		raw = value.Bytes
	case *pb.TypedData_Int:
		raw = []byte(fmt.Sprint(value.Int))
	case *pb.TypedData_Double:
		var err error
		raw, err = json.Marshal(value.Double)
		if err != nil {
			return reflect.Value{}, err
		}
	default:
		return reflect.Value{}, fmt.Errorf("unsupported host payload kind %T", data.Data)
	}
	if t.Kind() == reflect.String {
		return reflect.ValueOf(string(raw)).Convert(t), nil
	}
	if t.Kind() == reflect.Slice && t.Elem().Kind() == reflect.Uint8 {
		bytesValue := reflect.ValueOf(raw)
		if bytesValue.CanConvert(t) {
			return bytesValue.Convert(t), nil
		}
		// A slice of a defined uint8 type is not convertible from []byte.
		result := reflect.MakeSlice(t, len(raw), len(raw))
		for i, b := range raw {
			result.Index(i).SetUint(uint64(b))
		}
		return result, nil
	}
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) && t.Kind() != reflect.Map && t.Kind() != reflect.Slice && t.Kind() != reflect.Interface {
		return reflect.Value{}, fmt.Errorf("null is not valid for %s; use a pointer for nullable payloads", t)
	}
	target := reflect.New(t)
	if err := json.Unmarshal(raw, target.Interface()); err != nil {
		return reflect.Value{}, fmt.Errorf("decode JSON into %s: %w", t, err)
	}
	return target.Elem(), nil
}

func genericTriggerMetadata(in map[string]*pb.TypedData) map[string]any {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]any, len(in))
	for key, data := range in {
		if data == nil {
			continue
		}
		switch value := data.Data.(type) {
		case *pb.TypedData_String_:
			out[key] = value.String_
		case *pb.TypedData_Bytes:
			out[key] = value.Bytes
		case *pb.TypedData_Json:
			out[key] = json.RawMessage(value.Json)
		case *pb.TypedData_Int:
			out[key] = value.Int
		case *pb.TypedData_Double:
			out[key] = value.Double
		case *pb.TypedData_CollectionString:
			out[key] = value.CollectionString.GetString_()
		case *pb.TypedData_CollectionBytes:
			out[key] = value.CollectionBytes.GetBytes()
		case *pb.TypedData_CollectionSint64:
			out[key] = value.CollectionSint64.GetSint64()
		case *pb.TypedData_CollectionDouble:
			out[key] = value.CollectionDouble.GetDouble()
		}
	}
	return out
}

func bindingFailureResponse(requestID, invocationID string, err error) *pb.StreamingMessage {
	return &pb.StreamingMessage{RequestId: requestID, Content: &pb.StreamingMessage_InvocationResponse{
		InvocationResponse: &pb.InvocationResponse{InvocationId: invocationID, Result: &pb.StatusResult{
			Status: pb.StatusResult_Failure, Exception: &pb.RpcException{Source: "Binding conversion", Message: err.Error()},
		}},
	}}
}
