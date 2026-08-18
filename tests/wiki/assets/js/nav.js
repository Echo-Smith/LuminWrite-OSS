// ── 公共导航注入 ──────────────────────────────────────
// 在 <body> 开头自动插入导航栏，根据当前页面高亮对应链接
(function() {
  var path = window.location.pathname.split('/').pop() || 'index.html';

  var links = [
    { href: 'index.html',       label: '首页' },
    { href: 'about.html',       label: '介绍' },
    { href: 'architecture.html', label: '架构' },
    { href: 'tests.html',       label: '测试' },
    { href: 'changelog.html',   label: '版本' },
  ];

  var nav = document.createElement('nav');
  nav.className = 'site-nav';
  var html = '<a class="logo" href="index.html">笔润智谈 <span>V2 · Wiki</span></a><ul>';
  links.forEach(function(l) {
    var active = (l.href === path) ? ' class="active"' : '';
    html += '<li><a href="' + l.href + '"' + active + '>' + l.label + '</a></li>';
  });
  html += '</ul>';
  nav.innerHTML = html;

  // Insert as first child of <body>
  document.body.insertBefore(nav, document.body.firstChild);
})();
