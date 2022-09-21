# build stage
FROM golang:1-alpine AS build

RUN apk --no-cache add curl openssh-client

ENV \
  STRONGBOX_VERSION=master \
  KUSTOMIZE_VERSION=v4.5.5

RUN os=$(go env GOOS) && arch=$(go env GOARCH) \
  && curl -Ls https://github.com/kubernetes-sigs/kustomize/releases/download/kustomize/${KUSTOMIZE_VERSION}/kustomize_${KUSTOMIZE_VERSION}_${os}_${arch}.tar.gz \
    | tar xz -C /usr/local/bin/ \
  && chmod +x /usr/local/bin/kustomize \
  && go install github.com/uw-labs/strongbox@${STRONGBOX_VERSION}

ADD . /argocd-strongbox-plugin

WORKDIR /argocd-strongbox-plugin

RUN go build -o /argocd-strongbox-plugin

# final stage
FROM alpine:latest

RUN apk --no-cache add git openssh-client

COPY --from=build \
  /usr/local/bin/kustomize \
  /go/bin/strongbox \
  /usr/local/bin/

COPY --from=build /argocd-strongbox-plugin /usr/local/bin
