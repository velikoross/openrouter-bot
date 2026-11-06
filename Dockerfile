# Build image
FROM golang:1.25 AS build
WORKDIR /openrouter-bot
COPY go.mod go.sum ./
# Download dependencies for caching
RUN go mod download
COPY . .
# Build bot for determining os and architecture
ARG TARGETOS TARGETARCH
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build -o /openrouter-bot/openrouter-bot .

# Final image
FROM alpine:3.22
RUN apk add --no-cache ca-certificates tzdata
WORKDIR /openrouter-bot
# Translations are embedded into the binary; config.yaml holds defaults
COPY --from=build /openrouter-bot/config.yaml ./
COPY --from=build /openrouter-bot/openrouter-bot ./
# Directory for usage stats and runtime state (mount a volume here)
RUN mkdir logs
VOLUME ["/openrouter-bot/logs"]

ENTRYPOINT ["/openrouter-bot/openrouter-bot"]
