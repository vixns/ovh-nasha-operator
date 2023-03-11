FROM golang:alpine as app-builder
WORKDIR /go/src/app
COPY . .
RUN apk add git
# Static build required so that we can safely copy the binary over.
# `-tags timetzdata` embeds zone info from the "time/tzdata" package.
RUN CGO_ENABLED=0 go install -ldflags '-extldflags "-static"' -tags timetzdata
WORKDIR /go/src/app/route-fixer
RUN CGO_ENABLED=0 go install -ldflags '-extldflags "-static"' -tags timetzdata

FROM scratch
# the test program:
COPY --from=app-builder /go/bin/ovh-nasha-operator /ovh-nasha-operator
COPY --from=app-builder /go/bin/route-fixer /route-fixer
# the tls certificates:
# NB: this pulls directly from the upstream image, which already has ca-certificates:
COPY --from=alpine:latest /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
ENTRYPOINT ["/ovh-nasha-operator"]