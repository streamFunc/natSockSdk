module stun

go 1.18

require (
	github.com/gorilla/websocket v1.5.0
	github.com/pion/ice/v3 v3.0.2
	github.com/pion/logging v0.2.2
	github.com/pion/stun/v2 v2.0.0

)

require (
	github.com/klauspost/cpuid/v2 v2.2.6 // indirect
	github.com/klauspost/reedsolomon v1.12.0 // indirect
	github.com/pkg/errors v0.9.1 // indirect
	github.com/templexxx/cpufeat v0.0.0-20180724012125-cef66df7f161 // indirect
	github.com/templexxx/xor v0.0.0-20191217153810-f85b25db303b // indirect
	github.com/tjfoc/gmsm v1.4.1 // indirect
	github.com/xtaci/kcp-go v5.4.20+incompatible // indirect
	golang.org/x/mobile v0.0.0-20240520174638-fa72addaaa1b // indirect
)

require (
	github.com/davecgh/go-spew v1.1.1 // indirect
	github.com/google/uuid v1.4.0 // indirect
	github.com/pion/dtls/v2 v2.2.7 // indirect
	github.com/pion/mdns v0.0.9 // indirect
	github.com/pion/randutil v0.1.0 // indirect
	github.com/pion/transport/v2 v2.2.4 // indirect
	github.com/pion/transport/v3 v3.0.1 // indirect
	github.com/pion/turn/v3 v3.0.1 // indirect
	github.com/pmezard/go-difflib v1.0.0 // indirect
	github.com/stretchr/testify v1.8.4 // indirect
	github.com/xtaci/kcp-go/v5 v5.6.8
	golang.org/x/crypto v0.17.0 // indirect
	golang.org/x/net v0.19.0 // indirect
	golang.org/x/sys v0.20.0 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
)

replace github.com/pion/ice/v3 => ../ice

replace github.com/pion/transport/v3 => ../transport
