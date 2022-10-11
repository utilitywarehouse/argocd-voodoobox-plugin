# build stage
FROM golang:1 AS build

ENV \
  STRONGBOX_VERSION=master \
  KUSTOMIZE_VERSION=v4.5.5

RUN go install sigs.k8s.io/kustomize/kustomize/v4@${KUSTOMIZE_VERSION} \
  && go install github.com/uw-labs/strongbox@${STRONGBOX_VERSION}

ADD . /app

WORKDIR /app


RUN go test -v -cover ./... \
    && go build -o /argocd-strongbox-plugin .

# final stage
# argocd requires that sidecar container is running as user 999
FROM ubuntu:22.04

USER root

ENV ARGOCD_USER_ID=999

RUN groupadd -g $ARGOCD_USER_ID argocd && \
    useradd -r -u $ARGOCD_USER_ID -g argocd argocd && \
    apt-get update && \
    apt-get -y upgrade && \
    apt-get install -y git git-lfs && \
    apt-get clean && \
    rm -rf /var/lib/apt/lists/* /tmp/* /var/tmp/*

COPY --from=build \
  /go/bin/kustomize \
  /go/bin/strongbox \
  /usr/local/bin/

COPY --from=build /argocd-strongbox-plugin /usr/local/bin

ENV USER=argocd

USER $ARGOCD_USER_ID
