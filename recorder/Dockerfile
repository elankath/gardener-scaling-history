FROM ubuntu:latest
RUN apt update && apt -y install curl
WORKDIR /
COPY bin/recorder /recorder
RUN chmod +x recorder
EXPOSE 8080
ENTRYPOINT ["/recorder"]
#CMD ["/bin/bash", "-c", "/conntest || true; while true; do sleep 1000; done"]