# SPDX-License-Identifier: GPL-3.0-or-later
FROM golang:1.27-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -o /out/synccit ./cmd/synccit

FROM alpine:3.21
RUN adduser -D -H synccit && mkdir -p /data && chown synccit:synccit /data
COPY --from=build /out/synccit /usr/local/bin/synccit
USER synccit
ENV SYNCCIT_DB=/data/synccit.db SYNCCIT_ADDR=:8080
VOLUME /data
EXPOSE 8080
ENTRYPOINT ["synccit"]
CMD ["serve"]
