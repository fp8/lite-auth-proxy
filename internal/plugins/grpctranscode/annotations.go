package grpctranscode

import (
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

// httpBinding represents a parsed google.api.http annotation.
type httpBinding struct {
	method string // "GET", "POST", "PUT", "DELETE", "PATCH"
	path   string // path template, e.g. "/v1/messages/{name}"
	body   string // body field: "*", "", or field name
}

// parseHTTPAnnotation extracts the google.api.http option from a method descriptor.
// Returns nil if no annotation is present.
func parseHTTPAnnotation(md protoreflect.MethodDescriptor) *httpBinding {
	opts := md.Options()
	if opts == nil {
		return nil
	}

	// The google.api.http option is extension field 72295728 in MethodOptions.
	// Try the protoreflect API first (works if the extension is in the registry).
	raw := opts.ProtoReflect()
	httpField := raw.Descriptor().Fields().ByNumber(72295728)
	if httpField != nil && raw.Has(httpField) {
		httpMsg := raw.Get(httpField).Message()
		return extractHTTPRule(httpMsg)
	}

	// Fall back to scanning the wire-format bytes of the options directly.
	//
	// We must NOT re-parse into a fresh descriptorpb.MethodOptions and read only
	// GetUnknown(): when the google.api.http extension (field 72295728) is
	// registered in this binary's global proto registry — which it is whenever
	// any linked package imports the genproto annotations — proto.Unmarshal
	// resolves it as a *known* extension, so GetUnknown() comes back empty and the
	// binding is silently lost (this is exactly what happens for descriptors
	// rebuilt from server reflection). Instead, scan the marshaled options bytes,
	// which contain field 72295728 whether the extension is known or unknown.
	protoMsg, ok := opts.(proto.Message)
	if !ok {
		return nil
	}
	b, err := proto.Marshal(protoMsg)
	if err != nil || len(b) == 0 {
		return nil
	}
	return parseHTTPRuleFromUnknown(b)
}

// parseHTTPRuleFromUnknown is a minimal wire-format parser for the HttpRule extension.
// Field number 72295728, wire type 2 (length-delimited).
func parseHTTPRuleFromUnknown(data []byte) *httpBinding {
	targetTag := uint64(72295728<<3) | 2
	pos := 0
	for pos < len(data) {
		tag, n := decodeVarint(data[pos:])
		if n == 0 {
			break
		}
		pos += n
		wireType := tag & 0x7

		switch wireType {
		case 0: // varint
			_, n = decodeVarint(data[pos:])
			pos += n
		case 2: // length-delimited
			length, n := decodeVarint(data[pos:])
			pos += n
			if tag == targetTag {
				return parseHTTPRuleMessage(data[pos : pos+int(length)])
			}
			pos += int(length)
		case 5: // 32-bit
			pos += 4
		case 1: // 64-bit
			pos += 8
		default:
			return nil
		}
	}
	return nil
}

// parseHTTPRuleMessage parses the inner HttpRule message.
// Fields: get=2, put=3, post=4, delete=5, patch=6 (pattern oneof); body=7.
func parseHTTPRuleMessage(data []byte) *httpBinding {
	binding := &httpBinding{}
	pos := 0
	for pos < len(data) {
		tag, n := decodeVarint(data[pos:])
		if n == 0 {
			break
		}
		pos += n
		fieldNum := tag >> 3
		wireType := tag & 0x7

		switch wireType {
		case 0: // varint
			_, n = decodeVarint(data[pos:])
			pos += n
		case 2: // length-delimited
			length, n := decodeVarint(data[pos:])
			pos += n
			val := string(data[pos : pos+int(length)])
			pos += int(length)

			switch fieldNum {
			case 2:
				binding.method = "GET"
				binding.path = val
			case 3:
				binding.method = "PUT"
				binding.path = val
			case 4:
				binding.method = "POST"
				binding.path = val
			case 5:
				binding.method = "DELETE"
				binding.path = val
			case 6:
				binding.method = "PATCH"
				binding.path = val
			case 7:
				binding.body = val
			}
		case 5: // 32-bit
			pos += 4
		case 1: // 64-bit
			pos += 8
		default:
			return nil
		}
	}

	if binding.method == "" {
		return nil
	}
	return binding
}

// decodeVarint decodes a protobuf varint. Returns value and bytes consumed.
func decodeVarint(data []byte) (uint64, int) {
	var val uint64
	for i, b := range data {
		if i >= 10 {
			return 0, 0
		}
		val |= uint64(b&0x7f) << (uint(i) * 7)
		if b < 0x80 {
			return val, i + 1
		}
	}
	return 0, 0
}

// extractHTTPRule extracts method, path and body from an HttpRule protoreflect.Message.
func extractHTTPRule(msg protoreflect.Message) *httpBinding {
	binding := &httpBinding{}

	patternFields := []struct {
		num    protoreflect.FieldNumber
		method string
	}{
		{2, "GET"}, {3, "PUT"}, {4, "POST"}, {5, "DELETE"}, {6, "PATCH"},
	}
	for _, pf := range patternFields {
		fd := msg.Descriptor().Fields().ByNumber(pf.num)
		if fd != nil && msg.Has(fd) {
			binding.method = pf.method
			binding.path = msg.Get(fd).String()
			break
		}
	}
	if binding.method == "" {
		return nil
	}

	bodyFd := msg.Descriptor().Fields().ByNumber(7)
	if bodyFd != nil && msg.Has(bodyFd) {
		binding.body = msg.Get(bodyFd).String()
	}

	return binding
}
