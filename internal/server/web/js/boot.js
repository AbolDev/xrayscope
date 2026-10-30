// Runs before first paint: applies the saved theme, language and direction.
(function () {
  var theme, lang;
  try {
    theme = localStorage.getItem('xs.theme');
    lang = localStorage.getItem('xs.lang');
  } catch (e) {}
  if (!theme) theme = window.matchMedia && window.matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light';
  if (lang !== 'en' && lang !== 'fa') lang = 'en';
  var root = document.documentElement;
  root.setAttribute('data-theme', theme);
  root.setAttribute('lang', lang);
  root.setAttribute('dir', lang === 'fa' ? 'rtl' : 'ltr');
})();
