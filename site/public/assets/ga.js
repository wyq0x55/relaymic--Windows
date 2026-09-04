// GA4 初始化。单独成文件而不是内联，是为了让 CSP 继续不开 unsafe-inline ——
// script-src 只放行 self 和 googletagmanager，页面里任何注入的内联脚本照样被拦。
window.dataLayer = window.dataLayer || [];
function gtag(){dataLayer.push(arguments);}
gtag('js', new Date());
gtag('config', 'G-RMG8MNGGZQ');
