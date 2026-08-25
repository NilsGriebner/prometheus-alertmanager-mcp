FROM golang:1.27-alpine AS builder

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 go build -o /alertmanager-mcp .

FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=builder /alertmanager-mcp /alertmanager-mcp

ENTRYPOINT ["/alertmanager-mcp"]