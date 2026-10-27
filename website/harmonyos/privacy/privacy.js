  const button = document.getElementById('language');
  function showLanguage(lang) {
    const english = lang === 'en';
    document.getElementById('zh').hidden = english;
    document.getElementById('en').hidden = !english;
    document.documentElement.lang = english ? 'en' : 'zh-CN';
    document.title = english ? 'FnCPN for HarmonyOS Privacy Policy' : 'FnCPN 鸿蒙版隐私政策';
    button.textContent = english ? '中文' : 'English';
    button.setAttribute('aria-label', english ? '切换为中文' : 'Switch to English');
  }
  showLanguage(new URLSearchParams(location.search).get('lang') === 'en' ? 'en' : 'zh');
  button.hidden = false;
  button.addEventListener('click', () => {
    const lang = document.documentElement.lang === 'en' ? 'zh' : 'en';
    const url = new URL(location.href);url.searchParams.set('lang', lang);
    history.replaceState(null, '', url);showLanguage(lang);
  });
