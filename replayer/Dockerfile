FROM ubuntu:latest
# Install necessary tools
#RUN apt-get update && apt-get install -y \
#    curl \
#    tee
RUN apt update && apt -y install curl
WORKDIR /
COPY bin/remote /bin
COPY hack /hack
RUN mkdir /db && mkdir /reports
RUN chmod +x /bin && chmod +x /hack/*.sh
#EXPOSE 8080
#ENTRYPOINT ["/bin/bash", "-c", "/bin/replayer  | tee /reports/replayer-$(basename $INPUT_DATA_PATH).log"]
ENTRYPOINT ["/hack/pod-replayer-entrypoint.sh"]
