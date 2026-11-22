package contract

import (
	"embed"
	"fmt"
)

// swaggerUI holds the Swagger UI page and its vendored assets. Nothing is
// fetched from a CDN. The vendored files and their provenance are in
// swaggerui/vendor/VENDORED.md.
//
//go:embed swaggerui
var swaggerUI embed.FS

// SwaggerUIAsset is one file of the Swagger UI.
type SwaggerUIAsset struct {
	Name        string // file name under /swagger-ui/; "index.html" is the page itself
	ContentType string
	Body        []byte
}

// swaggerUIFiles is the closed list of files that are served: the page, its
// initializer and the two vendored files it loads. Licence files and
// provenance notes in the embed are not served.
var swaggerUIFiles = []struct{ name, path, contentType string }{
	{"index.html", "swaggerui/index.html", "text/html; charset=utf-8"},
	{"swagger-initializer.js", "swaggerui/swagger-initializer.js", "text/javascript; charset=utf-8"},
	{"swagger-ui-bundle.js", "swaggerui/vendor/swagger-ui-bundle.js", "text/javascript; charset=utf-8"},
	{"swagger-ui.css", "swaggerui/vendor/swagger-ui.css", "text/css; charset=utf-8"},
}

// SwaggerUIAssets returns the files that make up the Swagger UI, read from
// the embedded copy. The caller must not modify the bodies.
func SwaggerUIAssets() ([]SwaggerUIAsset, error) {
	assets := make([]SwaggerUIAsset, 0, len(swaggerUIFiles))
	for _, f := range swaggerUIFiles {
		body, err := swaggerUI.ReadFile(f.path)
		if err != nil {
			return nil, fmt.Errorf("embedded swagger ui file %s: %w", f.name, err)
		}
		assets = append(assets, SwaggerUIAsset{Name: f.name, ContentType: f.contentType, Body: body})
	}
	return assets, nil
}
