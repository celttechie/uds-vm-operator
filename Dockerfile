FROM alpine:3.19
RUN apk --no-cache add ca-certificates
WORKDIR /
COPY bin/uds-vm-operator /manager
USER 65532:65532
ENTRYPOINT ["/manager"]
