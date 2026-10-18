FROM golang:1.25-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /dq ./cmd/dq

FROM alpine:3.22
RUN adduser -D -u 10001 queue
USER queue
COPY --from=build /dq /usr/local/bin/dq
ENTRYPOINT ["dq"]
CMD ["help"]
