ARG SRC_DIR="/go/src/github.com/ceph/ceph-csi/"
ARG GO_ARCH
ARG BASE_IMAGE

FROM ${BASE_IMAGE} as updated_base

# TODO: remove the following cmd, when issues
# https://github.com/ceph/ceph-container/issues/2034
# https://github.com/ceph/ceph-container/issues/2141 are fixed.
RUN true \
     && . /etc/os-release \
     && OS_NAME="$ID" \
     && OS_VERSION="$(echo $VERSION_ID | cut -d. -f1)" \
     && (if [ "$OS_NAME" = centos -a "$OS_VERSION" = 8 ]; then find /etc/yum.repos.d/ -name '*.repo' -exec sed -i -e 's|^mirrorlist=|#mirrorlist=|g' -e 's|^#baseurl=http://mirror.centos.org|baseurl=https://vault.centos.org|g'  {} \; ; fi ) \
     && if [ ! -f /etc/yum.repos.d/ceph.repo -a "$CEPH_IS_DEVEL" = true ]; then if [ -z "$CEPH_SHA1" ]; then CEPH_SHA1="$(sed -n 's/.*CEPH_GIT_VER *= *"\(.*\)".*/\1/p' /usr/bin/ceph)"; fi ; yum reinstall -y "$(curl -fs "https://shaman.ceph.com/api/search/?project=ceph&distros=${OS_NAME}/${OS_VERSION}/$(uname -m)&flavor=default&ref=${CEPH_REF}&sha1=${CEPH_SHA1:-latest}" | jq -r .[0].url)/noarch/ceph-release-1-0.el${OS_VERSION}.noarch.rpm"; fi \
     && if [ ! -f /etc/yum.repos.d/ceph.repo -a "$CEPH_IS_DEVEL" != true ]; then yum reinstall -y "https://download.ceph.com/rpm-${CEPH_REF}/el${OS_VERSION}/noarch/ceph-release-1-1.el${OS_VERSION}.noarch.rpm"; fi \
     && ( dnf config-manager --disable tcmu-runner,tcmu-runner-source,tcmu-runner-noarch,ceph-iscsi,ganesha || true ) \
     && true

RUN mkdir -p /etc/selinux && touch /etc/selinux/config

RUN dnf -y update --nobest \
       && dnf -y install nfs-utils \
       && dnf clean all \
       && rm -rf /var/cache/yum

FROM updated_base as builder

LABEL stage="build"

ARG CSI_IMAGE_NAME=quay.io/cephcsi/cephcsi
ARG CSI_IMAGE_VERSION=canary
ARG GO_ARCH
ARG SRC_DIR
ARG GIT_COMMIT
ARG GOROOT=/usr/local/go

COPY build.env /

RUN source /build.env && \
    ( test -n "${GO_ARCH}" && exit 0; echo -e "\n\nMissing GO_ARCH argument for building image, install Golang or run: make image-cephcsi GOARCH=amd64\n\n"; exit 1 ) && \
    mkdir -p ${GOROOT} && \
    curl -L https://go.dev/dl/go${GOLANG_VERSION}.linux-${GO_ARCH}.tar.gz | tar xzf - -C ${GOROOT} --strip-components=1

# test if the downloaded version of Golang works (different arch?)
RUN ${GOROOT}/bin/go version && ${GOROOT}/bin/go env

# other/conflicting versions of protobuf get installed as dependency
RUN dnf -y --setopt=tsflags=noscripts remove protobuf

RUN dnf -y install --nodocs \
	librados-devel librbd-devel libcephfs-devel \
	/usr/bin/cc \
	make \
	git \
	&& dnf clean all \
	&& rm -rf /var/cache/yum \
    && true

ENV GOROOT=${GOROOT} \
    GOPATH=/go \
    CGO_ENABLED=1 \
    GIT_COMMIT="${GIT_COMMIT}" \
    ENV_CSI_IMAGE_VERSION="${CSI_IMAGE_VERSION}" \
    ENV_CSI_IMAGE_NAME="${CSI_IMAGE_NAME}" \
    PATH="${GOROOT}/bin:${GOPATH}/bin:${PATH}"


WORKDIR ${SRC_DIR}

# Copy source directories
COPY . ${SRC_DIR}

# Build executable
RUN make cephcsi

#-- Final container
FROM updated_base

ARG SRC_DIR

LABEL maintainers="Ceph-CSI Authors" \
    version=${CSI_IMAGE_VERSION} \
    architecture=${GO_ARCH} \
    description="Ceph-CSI Plugin"

COPY --from=builder ${SRC_DIR}/_output/cephcsi /usr/local/bin/cephcsi

# verify that all dynamically linked libraries are available
RUN [ $(ldd /usr/local/bin/cephcsi | grep -c '=> not found') = '0' ]

ENTRYPOINT ["/usr/local/bin/cephcsi"]
