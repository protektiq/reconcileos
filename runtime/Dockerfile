FROM golang:1.22-alpine
WORKDIR /app
COPY . .
RUN go build ./...
CMD ["sh", "-c", "echo Runtime scaffold image built"]
