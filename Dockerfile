FROM ubuntu:latest
WORKDIR /
COPY bin/recorder /recorder
RUN chmod +x recorder
EXPOSE 8080
ENTRYPOINT ["/recorder"]
#CMD ["/bin/bash", "-c", "/conntest || true; while true; do sleep 1000; done"]