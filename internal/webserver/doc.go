// Package webserver provides the embedded HTTP server for honey.
//
// @title Honey Web API
// @version 1.0
// @description REST API for the honey web UI (`honey web`). Authenticate with the same token printed at startup, or `HONEY_WEB_TOKEN`. WebSocket endpoints `GET /ws/ssh` and `GET /ws/pve-qemu-vnc` are not described by this OpenAPI document.
//
// @contact.name honey
//
// @host 127.0.0.1:8765
// @BasePath /
// @schemes http
//
// @securityDefinitions.apikey BearerAuth
// @in header
// @name Authorization
// @description Use `Authorization: Bearer <token>` where `<token>` is the web UI token.
//
// @securityDefinitions.apikey HoneyTokenHeader
// @in header
// @name X-Honey-Token
// @description Alternative header: `X-Honey-Token: <token>`.
//
// @securityDefinitions.apikey TokenQuery
// @in query
// @name token
// @description Optional query token (same value) for URLs: `?token=<token>`.
//
//go:generate go run github.com/swaggo/swag/v2/cmd/swag@latest init -g doc.go -o swaggerdocs --parseInternal --parseDependency -ot go,json
//go:generate go run ./cmd/swag2openapi -in swaggerdocs/swagger.json -out swaggerdocs/openapi.json
package webserver
