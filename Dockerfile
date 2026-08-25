FROM golang:1.27-alpine AS builder

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY main.go ./
COPY cmd/ cmd/
COPY internal/ internal/
RUN CGO_ENABLED=0 go build -o /alertmanager-mcp .

FROM alpine:3.24

COPY --from=builder /alertmanager-mcp /alertmanager-mcp

ENTRYPOINT ["/alertmanager-mcp"]