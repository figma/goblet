FROM golang:1.26-trixie AS build-env

RUN apt-get update && apt-get install -y gcc cmake pkg-config

ADD [".", "/app/"]

RUN ["/app/install_git2go.sh"]
RUN ["/app/build_goblet.sh"]
RUN ["/app/build_hooks.sh"]

FROM ubuntu:24.04
RUN apt-get update && apt-get install -y git
COPY --from=build-env ["/tmp/goblet-server", "/tmp/packobjectshook", "/git2go/static-build/build/CMakeCache.txt", "/app/example_config.json", "/app/"]
WORKDIR /app
RUN ["./goblet-server", "-config", "example_config.json", "-check"]
ENTRYPOINT ["./goblet-server"]
