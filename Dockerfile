FROM gcr.io/distroless/static:nonroot
WORKDIR /
COPY bin/uds-vm-operator /manager
USER 65532:65532
ENTRYPOINT ["/manager"]
