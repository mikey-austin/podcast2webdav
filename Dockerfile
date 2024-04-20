FROM golang:1.22-alpine

WORKDIR /usr/src/app

COPY go.mod go.sum ./
RUN go mod download && go mod verify

COPY . .
RUN go build -o /app ./cmd/podcast2webdav/main.go

EXPOSE 8080

CMD ["/app"]
