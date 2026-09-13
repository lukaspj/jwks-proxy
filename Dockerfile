FROM golang:1.24-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/jwks-proxy .

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/jwks-proxy /jwks-proxy
EXPOSE 8080
ENTRYPOINT ["/jwks-proxy"]
