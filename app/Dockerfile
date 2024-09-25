FROM ubuntu:latest
WORKDIR /
COPY bin/remote/app /bin/app
RUN chmod +x /bin/app
EXPOSE 8080
ENTRYPOINT ["/bin/app"]
#CMD ["/bin/bash", "-c", "/conntest || true; while true; do sleep 1000; done"]