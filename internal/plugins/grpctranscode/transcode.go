package grpctranscode

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/dynamicpb"
)

// problemJSON writes an RFC 9457 application/problem+json response.
func problemJSON(w http.ResponseWriter, httpStatus int, title, detail string) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(httpStatus)
	body := map[string]interface{}{
		"type":   "about:blank",
		"title":  title,
		"status": httpStatus,
		"detail": detail,
	}
	_ = json.NewEncoder(w).Encode(body)
}

// transcodeRequest handles a single transcoded REST->gRPC->REST request.
func transcodeRequest(
	w http.ResponseWriter,
	r *http.Request,
	entry *routeEntry,
	pathVars map[string]string,
	headerPrefix string,
	forwardAuthHeaders bool,
	requestTimeout time.Duration,
	marshalOpts protojson.MarshalOptions,
	unmarshalOpts protojson.UnmarshalOptions,
	logger *slog.Logger,
) {
	start := time.Now()

	// Build the request message.
	reqMsg := dynamicpb.NewMessage(entry.inputDesc)

	// Read body if applicable.
	var bodyBytes []byte
	if r.Body != nil && r.ContentLength != 0 && entry.bodyField != "" {
		var err error
		bodyBytes, err = io.ReadAll(r.Body)
		if err != nil {
			problemJSON(w, http.StatusBadRequest, "Bad Request", "failed to read request body")
			return
		}
	}

	// Populate from JSON body.
	if len(bodyBytes) > 0 {
		if entry.bodyField == "*" {
			if err := unmarshalOpts.Unmarshal(bodyBytes, reqMsg); err != nil {
				problemJSON(w, http.StatusBadRequest, "Bad Request", "invalid JSON body: "+err.Error())
				return
			}
		} else {
			// Unmarshal into a sub-message field.
			fd := entry.inputDesc.Fields().ByName(protoreflect.Name(entry.bodyField))
			if fd == nil {
				problemJSON(w, http.StatusInternalServerError, "Internal Server Error",
					"body field not found in message descriptor: "+entry.bodyField)
				return
			}
			if fd.Kind() == protoreflect.MessageKind {
				subMsg := dynamicpb.NewMessage(fd.Message())
				if err := unmarshalOpts.Unmarshal(bodyBytes, subMsg); err != nil {
					problemJSON(w, http.StatusBadRequest, "Bad Request", "invalid JSON body: "+err.Error())
					return
				}
				reqMsg.Set(fd, protoreflect.ValueOfMessage(subMsg))
			}
		}
	}

	// Populate from path variables.
	for name, val := range pathVars {
		fd := entry.inputDesc.Fields().ByName(protoreflect.Name(name))
		if fd != nil {
			setStringField(reqMsg, fd, val)
		}
	}

	// Populate from query parameters (only for fields not already set by body/path).
	for key, vals := range r.URL.Query() {
		fd := entry.inputDesc.Fields().ByName(protoreflect.Name(key))
		if fd == nil {
			continue
		}
		if _, inPath := pathVars[key]; inPath {
			continue
		}
		if len(vals) > 0 {
			setStringField(reqMsg, fd, vals[0])
		}
	}

	// Build gRPC metadata from auth headers.
	ctx := r.Context()
	if forwardAuthHeaders && headerPrefix != "" {
		md := metadata.MD{}
		for key, vals := range r.Header {
			if strings.HasPrefix(key, headerPrefix) {
				mdKey := strings.ToLower(key)
				md[mdKey] = vals
			}
		}
		if len(md) > 0 {
			ctx = metadata.NewOutgoingContext(ctx, md)
		}
	}

	// Apply request timeout.
	if requestTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, requestTimeout)
		defer cancel()
	}

	// Invoke the gRPC method.
	respMsg := dynamicpb.NewMessage(entry.outputDesc)
	err := entry.backend.conn.Invoke(ctx, entry.grpcMethod, reqMsg, respMsg)
	latency := time.Since(start)

	if err != nil {
		st, _ := status.FromError(err)
		code := st.Code()
		httpStatus := grpcToHTTPStatus(code)

		logger.Warn("grpc call failed",
			"grpc.method", entry.grpcMethod,
			"grpc.code", code.String(),
			"http.status", httpStatus,
			"latency_ms", latency.Milliseconds(),
		)

		problemJSON(w, httpStatus, code.String(), st.Message())
		return
	}

	// Marshal response to JSON.
	respBytes, err := marshalOpts.Marshal(respMsg)
	if err != nil {
		problemJSON(w, http.StatusInternalServerError, "Internal Server Error", "failed to marshal response")
		return
	}

	logger.Info("grpc call ok",
		"grpc.method", entry.grpcMethod,
		"latency_ms", latency.Milliseconds(),
	)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(respBytes)
}

// setStringField sets a proto field from a string value, converting types as needed.
func setStringField(msg *dynamicpb.Message, fd protoreflect.FieldDescriptor, val string) {
	switch fd.Kind() {
	case protoreflect.StringKind:
		msg.Set(fd, protoreflect.ValueOfString(val))
	case protoreflect.BoolKind:
		msg.Set(fd, protoreflect.ValueOfBool(val == "true"))
	case protoreflect.Int32Kind, protoreflect.Sint32Kind, protoreflect.Sfixed32Kind:
		if n, err := strconv.ParseInt(val, 10, 32); err == nil {
			msg.Set(fd, protoreflect.ValueOfInt32(int32(n)))
		}
	case protoreflect.Int64Kind, protoreflect.Sint64Kind, protoreflect.Sfixed64Kind:
		if n, err := strconv.ParseInt(val, 10, 64); err == nil {
			msg.Set(fd, protoreflect.ValueOfInt64(n))
		}
	case protoreflect.Uint32Kind, protoreflect.Fixed32Kind:
		if n, err := strconv.ParseUint(val, 10, 32); err == nil {
			msg.Set(fd, protoreflect.ValueOfUint32(uint32(n)))
		}
	case protoreflect.Uint64Kind, protoreflect.Fixed64Kind:
		if n, err := strconv.ParseUint(val, 10, 64); err == nil {
			msg.Set(fd, protoreflect.ValueOfUint64(n))
		}
	case protoreflect.FloatKind:
		if f, err := strconv.ParseFloat(val, 32); err == nil {
			msg.Set(fd, protoreflect.ValueOfFloat32(float32(f)))
		}
	case protoreflect.DoubleKind:
		if f, err := strconv.ParseFloat(val, 64); err == nil {
			msg.Set(fd, protoreflect.ValueOfFloat64(f))
		}
	case protoreflect.EnumKind:
		enumDesc := fd.Enum()
		if ev := enumDesc.Values().ByName(protoreflect.Name(val)); ev != nil {
			msg.Set(fd, protoreflect.ValueOfEnum(ev.Number()))
		} else if n, err := strconv.ParseInt(val, 10, 32); err == nil {
			msg.Set(fd, protoreflect.ValueOfEnum(protoreflect.EnumNumber(n)))
		}
	}
}
