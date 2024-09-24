FROM ubuntu:latest
WORKDIR /
COPY bin/remote/app /app
RUN chmod +x app
EXPOSE 8080
ENTRYPOINT ["/app"]
#CMD ["/bin/bash", "-c", "/conntest || true; while true; do sleep 1000; done"]