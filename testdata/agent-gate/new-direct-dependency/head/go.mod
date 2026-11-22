module example.com/fixture

go 1.27

require go.yaml.in/yaml/v3 v3.0.6

require (
	github.com/kept/direct v1.2.0
	github.com/added/direct v1.0.0
	github.com/kept/indirect v0.1.0 // indirect
)
require github.com/added/single v0.3.0
