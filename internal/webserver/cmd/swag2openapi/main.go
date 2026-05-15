// Command swag2openapi converts swag-generated Swagger 2.0 JSON to OpenAPI 3.x JSON for honey web.
package main

import (
	"encoding/json"
	"flag"
	"log"
	"os"

	"github.com/getkin/kin-openapi/openapi2"
	"github.com/getkin/kin-openapi/openapi2conv"
)

func main() {
	inPath := flag.String("in", "swaggerdocs/swagger.json", "input Swagger 2.0 JSON")
	outPath := flag.String("out", "swaggerdocs/openapi.json", "output OpenAPI 3 JSON")
	flag.Parse()
	in, err := os.ReadFile(*inPath)
	if err != nil {
		log.Fatal(err)
	}
	var doc openapi2.T
	if err := json.Unmarshal(in, &doc); err != nil {
		log.Fatal(err)
	}
	v3, err := openapi2conv.ToV3(&doc)
	if err != nil {
		log.Fatal(err)
	}
	out, err := json.MarshalIndent(v3, "", "  ")
	if err != nil {
		log.Fatal(err)
	}
	if err := os.WriteFile(*outPath, out, 0o600); err != nil {
		log.Fatal(err)
	}
}
