package handler

import (
	// import required packages
	_ "go.unistack.org/micro-proto/v5/openapiv3"
)

//go:generate sh -exc "curl -L https://github.com/swagger-api/swagger-ui/archive/refs/tags/v4.18.3.zip -o - | bsdtar -C swagger-ui --strip-components=2 -xv swagger-ui-4.18.3/dist && rm swagger-ui/*.map swagger-ui/*-es-*.js swagger-ui/swagger-ui.js swagger-ui/swagger-initializer.js"

//go:generate sh -exc "protoc -I./ -I$(go list -f '{{ .Dir }}' -m go.unistack.org/micro-proto/v5) --go-micro_out='components=micro|http|server',standalone=false,debug=true,paths=source_relative:./ ./meter/meter.proto"

//go:generate sh -exc "protoc -I./ -I$(go list -f '{{ .Dir }}' -m go.unistack.org/micro-proto/v5) --go-micro_out='components=micro|http|server',standalone=false,debug=true,paths=source_relative:./ ./health/health.proto"
