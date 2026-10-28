#!/bin/bash
# Runs only inside the disposable loong64 container, not on the host.
set -euo pipefail
test "$(dpkg --print-architecture)" = loong64
export DEBIAN_FRONTEND=noninteractive
if [ "${RUNTIME_ONLY:-0}" = 1 ]; then
 # Docker's post-dpkg hook would delete UI dependencies after Core installs,
 # making the later offline UI install impossible.
 rm -f /etc/apt/apt.conf.d/docker-clean
fi
# Prevent package installation from starting services in the container.
printf '#!/bin/sh\nexit 101\n' > /usr/sbin/policy-rc.d
chmod +x /usr/sbin/policy-rc.d
apt-get update
packages=(ca-certificates python3 binutils dbus dbus-user-session libpam-systemd \
  systemd-sysv systemd-resolved udev kmod linux-image-loong64 initramfs-tools \
  iproute2 iptables nftables wireguard-tools iputils-ping util-linux procps sudo)
if [ "${RUNTIME_ONLY:-0}" != 1 ]; then
 packages+=(gcc libc6-dev pkg-config git dpkg-dev desktop-file-utils \
  libgtk-3-dev libwebkit2gtk-4.1-dev libayatana-appindicator3-dev \
  xvfb xauth fonts-dejavu-core libgl1-mesa-dri)
fi
apt-get install -y --no-install-recommends "${packages[@]}"
record=packages.tsv
[ "${RUNTIME_ONLY:-0}" != 1 ] || record=runtime-base-packages.tsv
dpkg-query -W -f='${Package}\t${Version}\t${Architecture}\n' > "/workspace/dist/loongarch-validation/$record"
# The same rootfs boots with a real LoongArch kernel for the system checks.
# Explicitly include virtio drivers; host uname is irrelevant under qemu-user.
printf 'virtio_pci\nvirtio_blk\next4\n' >> /etc/initramfs-tools/modules
for modules in /lib/modules/*; do
  version=${modules##*/}
  update-initramfs -u -k "$version"
done
rm -f /usr/sbin/policy-rc.d
apt-get clean
