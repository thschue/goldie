module example.com/fixture

go 1.27

require go.yaml.in/yaml/v3 v3.0.6

require (
	github.com/kept/direct v1.2.0
	github.com/kept/indirect v0.1.0 // indirect
)

godebug tlsrsakex=1

require github.com/sneaky/indirect v0.1.0 // indirect

replace github.com/kept/direct => github.com/fork/direct v1.2.1

toolchain go1.27.1

tool github.com/sneaky/tool
