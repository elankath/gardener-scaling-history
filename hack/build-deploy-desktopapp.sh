#!/usr/bin/env zsh
set -eo pipefail

echoErr() { echo "$@" 1>&2; }


if [[ -z "$DESKTOP_VM_HOST" ]]; then
  echo "Kindly set your booked and fully-setup desktop VM host in env variable DESKTOP_VM_HOST before running this script"
  exit 1
fi
if whence -w  x86_64-unknown-linux-gnu-gcc > /dev/null 2>&1; then
    echo "x86_64-unknown-linux-gnu-gcc exists!  Will use for CGO Cross Compilation."
else
    echo "x86_64-unknown-linux-gnu-gcc does not exist. Installing via brew..."
    brew tap SergioBenitez/osxct
    brew install SergioBenitez/osxct/x86_64-unknown-linux-gnu
    echo "Installed x86_64-unknown-linux-gnu-gcc -> QUIT TERMINAL, OPEN FRESH TERMINAL AND RUN THIS SCRIPT: '$0' AGAIN"
    exit 3
fi
#brew install inetutils
if whence -w  sftp  > /dev/null 2>&1; then
    echo "sftp exists!  Will use for uploads."
else
    echo "sftp does not exist. Installing via brew..."
    brew install inetutils
    echo "Installed inetutils, OPEN FRESH TERMINAL AND RUN THIS SCRIPT: '$0' AGAIN"
    exit 3
fi

mkdir -p bin
BINOUT=bin/remote/desktopapp
#if [[ -f "${BINOUT}" ]]; then
#  echo "Removing existing desktopapp remote binary."
#  rm "$BINOUT"
#fi
echo "Building desktopapp for linux/amd64..."
#CC=x86_64-unknown-linux-gnu-gcc CGO_ENABLED=1 GOOS=linux GOARCH=amd64 go build -v -o bin/remote/recorder cmd/recorder/main.go
GOOS=linux GOARCH=amd64 go build -v -o "$BINOUT" cmd/desktopapp/main.go
chmod +x bin/remote/desktopapp

echo "Performing pre-requisities on desktop VM ${DESKTOP_VM_HOST}..."
ssh "scalehist@${DESKTOP_VM_HOST}" 'mkdir -p ~/bin;mkdir -p ~/logs;mkdir -p ~/gen;echo "Directories created"'
echo "Coping $BINOUT to ${DESKTOP_VM_HOST}/bin..."
echo scp -C -o "IPQoS=throughput" "${BINOUT}" "scalehist@${DESKTOP_VM_HOST}:~/bin"
scp -C -o "IPQoS=throughput" "${BINOUT}" "scalehist@${DESKTOP_VM_HOST}:~/bin"

#echo "Using sftp to upload $BINOUT to scalehist@${DESKTOP_VM_HOST}/bin..."
#sftp "scalehist@${DESKTOP_VM_HOST}" <<EOF
#cd bin
#put "$BINOUT
#bye
#EOF
