FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /out/xrayscope ./cmd/xrayscope

FROM alpine:3.20
RUN apk add --no-cache ca-certificates tzdata && adduser -D -u 10001 xs
COPY --from=build /out/xrayscope /usr/local/bin/xrayscope
USER xs
ENV XRAYSCOPE_DATA_DIR=/data
VOLUME ["/data"]
EXPOSE 8686
# Mount the panel database and the Xray log read-only, for example:
#   -v /etc/x-ui:/etc/x-ui:ro -v /var/log/xray:/var/log/xray:ro
ENTRYPOINT ["xrayscope"]
CMD ["serve", "-c", "/data/config.json"]
