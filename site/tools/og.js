// 和站点用同一段合成包络的形状，OG 图和落地页看起来才是同一个东西
  const amps = [.18,.34,.52,.71,.86,.64,.45,.3,.52,.78,.94,.72,.5,.33,.2,.38,.6,.82,.66,.44,.26,.14];
  const w = document.getElementById('w');
  for (const a of amps) {
    const i = document.createElement('i');
    i.style.height = (4 + a * 42).toFixed(0) + 'px';   // 脚本设的样式不受 style-src 限制
    w.appendChild(i);
  }
