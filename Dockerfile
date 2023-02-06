# build stage
FROM golang:1 AS build

ENV \
  STRONGBOX_VERSION=1.0.1 \
  KUSTOMIZE_VERSION=v5.0.0

RUN os=$(go env GOOS) && arch=$(go env GOARCH) \
  && curl -Ls https://github.com/kubernetes-sigs/kustomize/releases/download/kustomize/${KUSTOMIZE_VERSION}/kustomize_${KUSTOMIZE_VERSION}_${os}_${arch}.tar.gz \
    | tar xz -C /usr/local/bin/ \
  && chmod +x /usr/local/bin/kustomize \
  && curl -Ls https://github.com/uw-labs/strongbox/releases/download/v${STRONGBOX_VERSION}/strongbox_${STRONGBOX_VERSION}_${os}_${arch} \
    > /usr/local/bin/strongbox \
  && chmod +x  /usr/local/bin/strongbox

ADD . /app

WORKDIR /app


RUN go test -v -cover ./... \
    && go build -ldflags='-s -w' -o /argocd-voodoobox-plugin .

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
  /usr/local/bin/kustomize \
  /usr/local/bin/strongbox \
  /argocd-voodoobox-plugin \
  /usr/local/bin/

ENV USER=argocd

USER $ARGOCD_USER_ID
