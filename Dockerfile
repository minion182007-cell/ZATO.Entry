FROM golang:1.24-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /zato-entry ./cmd/zato-entry

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /zato-entry /zato-entry
EXPOSE 8080
ENTRYPOINT ["/zato-entry"]
