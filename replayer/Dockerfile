FROM ubuntu:latest
# Install necessary tools
#RUN apt-get update && apt-get install -y \
#    curl \
#    tee
RUN apt update && apt -y install curl
WORKDIR /
COPY bin/remote /bin
RUN mkdir /db
RUN chmod +x /bin
#EXPOSE 8080
#ENTRYPOINT ["/bin/replayer"]
#ENTRYPOINT ["/bin/bash", "-c", "while true; do sleep 1000; done"]
ENTRYPOINT ["/bin/bash", "-c", "/bin/replayer  | tee /reports/replayer-$(basename $INPUT_DATA_PATH).log"]
# ENTRYPOINT ["/bin/sh", "-c", "/usr/local/bin/myapp | tee /var/log/myapp/output-$(basename $INPUT_DATA_PATH).log"]
