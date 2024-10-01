FROM ubuntu:latest
RUN apt update && apt -y install curl
WORKDIR /
COPY bin/remote/recorder /bin/recorder
RUN chmod +x /bin/recorder
EXPOSE 8080
ENTRYPOINT ["/bin/recorder"]
#CMD ["/bin/bash", "-c", "/conntest || true; while true; do sleep 1000; done"]