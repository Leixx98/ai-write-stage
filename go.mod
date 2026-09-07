module github.com/Leixx98/ai-write-stage

go 1.25.5

require (
	github.com/coder/websocket v1.8.15
	github.com/gofrs/flock v0.13.0
	github.com/voocel/agentcore v1.8.1
	github.com/voocel/litellm v1.8.9
	github.com/yuin/goldmark v1.7.16
	golang.org/x/text v0.40.0
)

require (
	golang.org/x/image v0.44.0 // indirect
	golang.org/x/sys v0.38.0 // indirect
)

replace github.com/voocel/litellm => ./third-party/litellm
