#############      builder                                  #############
FROM golang:1.22.0 AS builder

WORKDIR /go/src/github.com/scalehist
COPY . .

RUN go build -o bin/recorder cmd/recorder/main.go

#############      base                                     #############
#FROM gcr.io/distroless/static-debian11:nonroot as base
FROM ubuntu:latest as base
WORKDIR /

#############      recorder               #############
FROM base AS scalehist

COPY --from=builder /go/src/github.com/scalehist/bin/scalehist /scalehist
ENV CONFIG_DIR=/tmp/kubeconfig
ENV DB_DIR=/tmp/db
ENTRYPOINT ["/scalehist"]
