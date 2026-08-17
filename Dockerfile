FROM golang:1.23-alpine AS build

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 go build \
    -trimpath \
    -ldflags="-s -w" \
    -o /out/document-decryptor \
    ./cmd/decryptor

FROM alpine:3.20

RUN apk add --no-cache qpdf ca-certificates \
    && addgroup -S decryptor \
    && adduser -S -G decryptor -H -h /nonexistent -s /sbin/nologin decryptor

COPY --from=build /out/document-decryptor /usr/local/bin/document-decryptor

USER decryptor:decryptor

EXPOSE 8080

ENTRYPOINT ["/usr/local/bin/document-decryptor"]
