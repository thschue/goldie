module example.com/fixture

go 1.27

require go.yaml.in/yaml/v4 v4.0.0

require (
	github.com/kept/direct/v2 v2.0.0
	github.com/added/direct v1.0.0
	github.com/kept/indirect v0.1.0 // indirect
)
require github.com/added/single v0.3.0
