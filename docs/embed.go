package docs

import _ "embed"

//go:embed swagger.html
var SwaggerHTML []byte

//go:embed openapi.yaml
var OpenAPIYAML []byte
