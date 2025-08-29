FROM golang:1.24 as builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o /manager main.go

FROM gcr.io/distroless/static:nonroot
COPY --from=builder /manager /manager
USER nonroot:nonroot
ENTRYPOINT ["/manager"]
