FROM golang:1.24.2-alpine
WORKDIR /app
COPY go.mod ./
COPY go.sum ./
RUN go mod download
COPY . .
RUN go build -o auth-service .
EXPOSE 8081
CMD ["./auth-service"]