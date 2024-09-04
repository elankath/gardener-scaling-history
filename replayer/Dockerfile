FROM ubuntu:latest
WORKDIR /
COPY bin/remote /bin
RUN chmod +x /bin
#EXPOSE 8080
ENTRYPOINT ["/bin/replayer"]
#CMD ["/bin/bash", "-c", "while true; do sleep 1000; done"]