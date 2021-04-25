package http

import (
	"context"
	"fmt"
	"reflect"
	"unicode"
	"unicode/utf8"

	"github.com/unistack-org/micro/v3/server"
)

type methodType struct {
	ArgType     reflect.Type
	ReplyType   reflect.Type
	ContextType reflect.Type
	method      reflect.Method
	stream      bool
}

// Is this an exported - upper case - name?
func isExported(name string) bool {
	r, _ := utf8.DecodeRuneInString(name)
	return unicode.IsUpper(r)
}

// Is this type exported or a builtin?
func isExportedOrBuiltinType(t reflect.Type) bool {
	for t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	// PkgPath will be non-empty even for an exported type,
	// so we need to check the type name as well.
	return isExported(t.Name()) || t.PkgPath() == ""
}

// prepareEndpoint() returns a methodType for the provided method or nil
// in case if the method was unsuitable.
func prepareEndpoint(method reflect.Method) (*methodType, error) {
	mtype := method.Type
	mname := method.Name
	var replyType, argType, contextType reflect.Type
	var stream bool

	// Endpoint() must be exported.
	if method.PkgPath != "" {
		return nil, fmt.Errorf("Endpoint must be exported")
	}

	switch mtype.NumIn() {
	case 3:
		// assuming streaming
		argType = mtype.In(2)
		contextType = mtype.In(1)
		stream = true
	case 4:
		// method that takes a context
		argType = mtype.In(2)
		replyType = mtype.In(3)
		contextType = mtype.In(1)
	default:
		return nil, fmt.Errorf("method %v of %v has wrong number of ins: %v", mname, mtype, mtype.NumIn())
	}

	if stream {
		// check stream type
		streamType := reflect.TypeOf((*server.Stream)(nil)).Elem()
		if !argType.Implements(streamType) {
			return nil, fmt.Errorf("%v argument does not implement Streamer interface: %v", mname, argType)
		}
	} else {
		// if not stream check the replyType

		// First arg need not be a pointer.
		if !isExportedOrBuiltinType(argType) {
			return nil, fmt.Errorf("%v argument type not exported: %v", mname, argType)
		}

		if replyType.Kind() != reflect.Ptr {
			return nil, fmt.Errorf("method %v reply type not a pointer: %v", mname, replyType)
		}

		// Reply type must be exported.
		if !isExportedOrBuiltinType(replyType) {
			return nil, fmt.Errorf("method %v reply type not exported: %v", mname, replyType)
		}
	}

	// Endpoint() needs one out.
	if mtype.NumOut() != 1 {
		return nil, fmt.Errorf("method %v has wrong number of outs: %v", mname, mtype.NumOut())
	}
	// The return type of the method must be error.
	if returnType := mtype.Out(0); returnType != typeOfError {
		return nil, fmt.Errorf("method %v returns %v not error", mname, returnType.String())
	}

	return &methodType{method: method, ArgType: argType, ReplyType: replyType, ContextType: contextType, stream: stream}, nil
}

func (m *methodType) prepareContext(ctx context.Context) reflect.Value {
	if contextv := reflect.ValueOf(ctx); contextv.IsValid() {
		return contextv
	}
	return reflect.Zero(m.ContextType)
}
