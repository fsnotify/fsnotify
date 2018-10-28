FROM golang:latest

WORKDIR /go/src/github.com/fsnotify/fsnotify
COPY . .

RUN go get -d -v ./...

# docker build -t fsnotify .
# docker run --rm fsnotify go test