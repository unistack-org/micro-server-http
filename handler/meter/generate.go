package meter

//go:generate sh -c "protoc -I./ -I$(go list -f '{{ .Dir }}' -m go.unistack.org/micro-proto/v4) --go-micro_out='components=micro|http|server',standalone=false,debug=true,paths=source_relative:./ meter.proto"

import (
	// import required packages
	_ "go.unistack.org/micro-proto/v4/openapiv3"
)
