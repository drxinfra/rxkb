# Статический бинарник и образ из scratch: внутри только rxkb и лицензия. Исходящих соединений
# у сервера нет, поэтому нет и корневых сертификатов. База знаний в образ не входит:
# смонтируйте каталог с kb.json в /data только для чтения.
FROM golang:1.27.3-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /rxkb .

FROM scratch
LABEL io.modelcontextprotocol.server.name="io.github.drxinfra/rxkb"
LABEL org.opencontainers.image.source="https://github.com/drxinfra/rxkb"
LABEL org.opencontainers.image.description="MCP server with the drxinfra.ru knowledge base for Directum RX"
LABEL org.opencontainers.image.licenses="Apache-2.0"
COPY --from=build /rxkb /rxkb
COPY LICENSE NOTICE /
USER 65532:65532
ENV RXKB_LISTEN=0.0.0.0:8790 RXKB_FILE=/data/kb.json
EXPOSE 8790
VOLUME /data
ENTRYPOINT ["/rxkb"]
