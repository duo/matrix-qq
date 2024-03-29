FROM golang:1.20-alpine AS builder

RUN apk add --no-cache git ca-certificates build-base olm-dev

WORKDIR /build

COPY ./ .

RUN set -ex \
	&& cd /build \
	&& go build -o matrix-qq

FROM alpine:latest

RUN apk add --no-cache --update --quiet --no-progress tzdata ffmpeg ca-certificates olm \
	&& cp /usr/share/zoneinfo/Asia/Shanghai /etc/localtime \
	&& echo "Asia/Shanghai" > /etc/timezone

COPY --from=builder /build/matrix-qq /usr/bin/matrix-qq
COPY --from=builder /build/example-config.yaml /opt/matrix-qq/example-config.yaml
COPY --from=builder /build/docker-run.sh /docker-run.sh

VOLUME /data
WORKDIR /data

CMD ["/docker-run.sh"]
