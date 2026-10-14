const routes = {
  lan: ['LOCAL NETWORK', '本地局域网', '就在同一个网络里。', '电脑与 NAS 在同一局域网时，自动选择局域网直达，使用 NAS 局域网地址访问。'],
  ipv6: ['DIRECT CONNECTION', 'IPv6 直连', '跨越距离，直接相连。', '在远程网络中，网络条件允许时尝试 IPv6 直连，通过加密隧道连接NAS 所在的网络。'],
  relay: ['FN CONNECT RELAY', '', '直连之外，还有一条路。', '无法直连时，通过 fnOS 官方中转服务器连接 NAS，无需自建中转。中继速度取决于官方服务权益与网络条件。']
};
const tabs = [...document.querySelectorAll('[data-route]')];
function selectRoute(tab) {
  tabs.forEach(item => { item.setAttribute('aria-selected', String(item === tab)); item.tabIndex = item === tab ? 0 : -1; });
  const relay = tab.dataset.route === 'relay';
  ['relay-server', 'relay-wire', 'relay-info'].forEach(id => { document.getElementById(id).hidden = !relay; });
  document.querySelector('.route-diagram').classList.toggle('is-relay', relay);
  document.querySelector('#route-panel').setAttribute('aria-labelledby', tab.id);
  ['route-label', 'route-middle', 'route-title', 'route-description'].forEach((id, i) => { document.getElementById(id).textContent = routes[tab.dataset.route][i]; });
}
tabs.forEach((tab, index) => {
  tab.addEventListener('click', () => selectRoute(tab));
  tab.addEventListener('keydown', event => {
    let next;
    if (event.key === 'ArrowRight' || event.key === 'ArrowDown') next = (index + 1) % tabs.length;
    if (event.key === 'ArrowLeft' || event.key === 'ArrowUp') next = (index + tabs.length - 1) % tabs.length;
    if (event.key === 'Home') next = 0;
    if (event.key === 'End') next = tabs.length - 1;
    if (next !== undefined) { event.preventDefault(); selectRoute(tabs[next]); tabs[next].focus(); }
  });
});
let toastTimer;
document.querySelector('#copy-command').addEventListener('click', async () => {
  const toast = document.querySelector('#toast');
  try {
    await navigator.clipboard.writeText('brew tap rectcircle/fn-connect-private-network https://github.com/rectcircle/fn-connect-private-network\nbrew install --cask rectcircle/fn-connect-private-network/fncpn');
    toast.textContent = '已复制完整安装命令（包含 brew tap）';
  } catch { toast.textContent = '无法自动复制，请从安装文档复制完整命令。'; }
  toast.classList.add('visible'); clearTimeout(toastTimer); toastTimer = setTimeout(() => toast.classList.remove('visible'), 4000);
});
