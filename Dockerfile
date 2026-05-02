FROM golang:1.24-alpine AS build
WORKDIR /src
COPY go.mod ./
COPY cmd ./cmd
COPY internal ./internal
RUN go build -o /out/llmproxy ./cmd/llmproxy

FROM alpine:3.22
RUN adduser -D -H -u 10001 llmproxy
USER 10001:10001
COPY --from=build /out/llmproxy /usr/local/bin/llmproxy
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/llmproxy"]
