#############      builder                                  #############
FROM golang:1.22.0 AS builder

WORKDIR /src
COPY go.mod go.sum ./

RUN go mod download

COPY . .

RUN go build -o /bin/recorder cmd/recorder/main.go

#############      base                                     #############
#FROM gcr.io/distroless/static-debian11:nonroot as base
FROM ubuntu:latest

WORKDIR /

#############      recorder               #############
#FROM base AS scalehist

COPY --from=builder /bin/recorder /recorder
COPY --from=builder /src/gen /gen
ENV CONFIG_DIR=/gen
ENV DB_DIR=/gen
ENTRYPOINT ["/recorder"]
#CMD ["/bin/bash", "-c", "/recorder || true; while true; do sleep 1000; done"]
