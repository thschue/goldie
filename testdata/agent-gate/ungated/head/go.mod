module example.com/fixture

go 1.27

require go.yaml.in/yaml/v4 v4.0.0

require (
	github.com/kept/direct v1.3.0
	github.com/kept/indirect v0.1.0 // indirect
	github.com/added/indirect v0.2.0 // indirect
)
