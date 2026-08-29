FROM alpine:latest

RUN apk add --no-cache ca-certificates

WORKDIR /root/
COPY bin/app .

EXPOSE 3000
CMD ["./app"]
