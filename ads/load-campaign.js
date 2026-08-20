// RelayMic 投放素材一次性灌入。在 Google Ads 后台
// Tools → Bulk actions → Scripts 里新建脚本，粘贴后先 Preview 再 Run。
//
// 幂等：已存在的广告组/关键词会跳过，重复跑不会灌出两份。
// 广告系列保持暂停状态，这个脚本不碰它的启停。

var CAMPAIGN_NAME = "RelayMic - Search - Test";
var CPC = 1.5;

function main() {
  var it = AdsApp.campaigns().withCondition("Name = '" + CAMPAIGN_NAME + "'").get();
  if (!it.hasNext()) { throw new Error('找不到广告系列：' + CAMPAIGN_NAME); }
  var campaign = it.next();
  Logger.log('广告系列：' + campaign.getName() + '（' + campaign.getId() + '）');

  checkLocations(campaign);
  addCampaignNegatives(campaign);
  GROUPS.forEach(function (g) { buildGroup(campaign, g); });
  Logger.log('完成。');
}

function existingAdGroup(campaign, name) {
  var it = campaign.adGroups().withCondition("Name = '" + name + "'").get();
  return it.hasNext() ? it.next() : null;
}

function buildGroup(campaign, g) {
  var adGroup = existingAdGroup(campaign, g.name);
  if (!adGroup) {
    var op = campaign.newAdGroupBuilder().withName(g.name).withCpc(CPC).build();
    if (!op.isSuccessful()) {
      Logger.log('建组失败 ' + g.name + '：' + op.getErrors().join('; '));
      return;
    }
    adGroup = op.getResult();
    Logger.log('新建广告组：' + g.name);
  } else {
    Logger.log('广告组已存在，沿用：' + g.name);
  }

  // 关键词：已有的跳过，避免重复跑时灌两份
  var have = {};
  var kit = adGroup.keywords().get();
  while (kit.hasNext()) { have[kit.next().getText()] = true; }

  var added = 0;
  g.keywords.forEach(function (k) {
    if (have[k]) { return; }
    var op = adGroup.newKeywordBuilder().withText(k).build();
    if (op.isSuccessful()) { added++; } else {
      Logger.log('  关键词失败 ' + k + '：' + op.getErrors().join('; '));
    }
  });
  Logger.log('  关键词 +' + added + '（组内共 ' + g.keywords.length + '）');

  // 每组只建一条 RSA；已有广告就不再建，免得重复跑堆一堆
  if (adGroup.ads().get().totalNumEntities() > 0) {
    Logger.log('  已有广告，跳过 RSA');
    return;
  }
  var adOp = adGroup.newAd().responsiveSearchAdBuilder()
    .withHeadlines(g.headlines)
    .withDescriptions(g.descriptions)
    .withFinalUrl(g.finalUrl)
    .build();
  Logger.log(adOp.isSuccessful() ? '  RSA 已建' : '  RSA 失败：' + adOp.getErrors().join('; '));
}

// 地区只核对，不设置 —— 这里不是偷懒，是 Scripts 写不进去。
//
// campaign.addLocation() 三种写法全失败：
//   addLocation(2826)             → InputError
//   addLocation("United Kingdom") → InputError，提示要 TargetedLocation 或 { id, bidModifier }
//   addLocation({ id: 2826 })     → 格式收了，照样 "An error occurred"
// 根因是广告系列当时挂在「United States and Canada」这个预设选项上而不是自定义地区列表，
// 预设 Scripts 改不动。只能在界面里改一次：广告系列设置 → Locations → Enter another
// location → Advanced search → Add locations in bulk，贴国家名 → Target all → 保存。
// 设完就不用再动，所以这里只负责发现它没设。
function checkLocations(campaign) {
  var have = {};
  var it = campaign.targeting().targetedLocations().get();
  while (it.hasNext()) { have[String(it.next().getId())] = true; }
  var missing = LOCATIONS.filter(function (l) { return !have[String(l.id)]; });
  Logger.log(missing.length
    ? '地区缺 ' + missing.length + ' 个，去界面补：' + missing.map(function (l) { return l.name; }).join('、')
    : '地区 ' + LOCATIONS.length + ' 个齐了');
}

function addCampaignNegatives(campaign) {
  var have = {};
  var it = campaign.negativeKeywords().get();
  while (it.hasNext()) { have[it.next().getText()] = true; }
  var n = 0;
  NEGATIVES.forEach(function (t) {
    var phrase = '"' + t + '"';          // 词组匹配，比广泛否定精确
    if (!have[phrase]) { campaign.createNegativeKeyword(phrase); n++; }
  });
  Logger.log('广告系列否定词 +' + n);
}

var LOCATIONS = [
  { id: 2840, name: 'United States' },
  { id: 2124, name: 'Canada' },
  { id: 2826, name: 'United Kingdom' },
  { id: 2036, name: 'Australia' },
  { id: 2276, name: 'Germany' },
  { id: 2528, name: 'Netherlands' },
  { id: 2724, name: 'Spain' },
  { id: 2484, name: 'Mexico' },
  { id: 2032, name: 'Argentina' },
];

var NEGATIVES = [
  "free",
  "gratis",
  "crack",
  "cracked",
  "torrent",
  "warez",
  "nulled",
  "pirate",
  "pirata",
  "job",
  "jobs",
  "hiring",
  "career",
  "salary",
  "empleo",
  "trabajo",
  "vacante",
  "continuity",
  "continuity camera",
  "continuity microphone",
  "camara de continuity",
  "camara continuity",
  "iphone continuity",
  "same room iphone",
  "airpods continuity",
  "windows to windows",
  "windows rdp microphone",
  "rdp windows to windows",
  "microsoft rdp windows",
  "remote desktop windows only",
  "fix microphone",
  "repair microphone",
  "microphone repair",
  "reparar micrófono",
  "microphone driver",
  "audio driver",
  "usb microphone",
  "best usb mic",
  "blue yeti",
  "buy microphone",
  "microphone review",
  "macbook microphone broken",
  "internal microphone",
  "hardware failure",
  "macbook microphone not working",
  "imac microphone not working",
  "built in microphone not working",
  "internal microphone not working",
  "laptop microphone not working",
  "my mac microphone not working",
  "iphone microphone not working",
  "micrófono interno no funciona",
  "micrófono del mac no funciona",
  "micrófono del macbook no funciona",
  "micrófono integrado no funciona",
];

var GROUPS = [
  {
    name: "Remote Mac Core",
    finalUrl: "https://relaymic.com/?ref=core",
    keywords: [
      "[remote desktop microphone mac]",
      "\"remote desktop microphone mac\"",
      "[use microphone over remote desktop mac]",
      "\"use microphone over remote desktop mac\"",
      "[microphone passthrough remote desktop]",
      "\"microphone passthrough remote desktop\"",
      "[microphone passthrough remote desktop mac]",
      "\"microphone passthrough remote desktop mac\"",
      "[remote mac microphone]",
      "\"remote mac microphone\"",
      "[how to use microphone remote desktop mac]",
      "\"how to use microphone remote desktop mac\"",
      "[forward microphone remote desktop mac]",
      "\"forward microphone remote desktop mac\"",
      "[send microphone to remote mac]",
      "\"send microphone to remote mac\"",
    ],
    headlines: [
      "Mic for a Remote Mac",
      "Use Your Mic on Remote Mac",
      "Remote Mac Mic Passthrough",
      "Speak Into a Remote Mac",
      "Add Voice to Remote Mac",
      "Real Mic on the Remote Mac",
      "Browser to Mac Microphone",
      "Your Voice on the Remote Mac",
      "Remote Desktop Needs a Mic",
      "Make Voice Reach Your Mac",
      "Mic Input for Remote Mac",
      "Control Mac and Use Your Mic",
      "RelayMic for Remote Macs",
      "Try RelayMic for 7 Days",
      "Get RelayMic Early Access",
    ],
    descriptions: [
      "Send your voice from any browser to a remote Mac as a real system microphone.",
      "Remote desktop moves screen, keyboard and mouse. RelayMic adds your microphone.",
      "Install on the remote Mac, open relaymic.com, enter a 6-digit code, and speak.",
      "48 kHz Opus stereo, direct when possible, and zero bytes of your audio stored.",
    ],
  },
  {
    name: "Remote Support Tools",
    finalUrl: "https://relaymic.com/?ref=support-tools",
    keywords: [
      "[anydesk microphone not working mac]",
      "\"anydesk microphone not working mac\"",
      "[anydesk microphone mac]",
      "\"anydesk microphone mac\"",
      "[use microphone anydesk mac]",
      "\"use microphone anydesk mac\"",
      "[teamviewer microphone remote mac]",
      "\"teamviewer microphone remote mac\"",
      "[teamviewer microphone transmission]",
      "\"teamviewer microphone transmission\"",
      "[teamviewer microphone mac]",
      "\"teamviewer microphone mac\"",
      "[remote support microphone mac]",
      "\"remote support microphone mac\"",
    ],
    headlines: [
      "Mic Missing in Your Session",
      "Use Your Mic on Remote Mac",
      "Add a Mic to Remote Access",
      "Relay Voice Into Your Mac",
      "Remote Mac Mic Passthrough",
      "Real Mic for the Remote Mac",
      "Speak Into Your Remote Mac",
      "Remote Session Needs a Mic",
      "Your Browser Becomes the Mic",
      "Keep Your Tool, Add a Mic",
      "Mic for Mac Remote Sessions",
      "Works Beside Any Remote App",
      "Voice for Remote Mac Support",
      "7-Day RelayMic Trial",
      "Get RelayMic Early Access",
    ],
    descriptions: [
      "Your remote tool moves screen and keyboard. RelayMic moves your voice to the Mac.",
      "Type a 6-digit code, speak, then choose RelayMic in the Mac app you need.",
      "Runs beside whatever remote software you already use. Nothing to reconfigure.",
      "Try it for 7 days. Plans are $4.99 monthly or $49 yearly for up to 3 Macs.",
    ],
  },
  {
    name: "Dev Streaming Tools",
    finalUrl: "https://relaymic.com/?ref=streaming-tools",
    keywords: [
      "[parsec microphone mac]",
      "\"parsec microphone mac\"",
      "[parsec mic passthrough mac]",
      "\"parsec mic passthrough mac\"",
      "[rustdesk microphone]",
      "\"rustdesk microphone\"",
      "[rustdesk microphone mac]",
      "\"rustdesk microphone mac\"",
      "[jump desktop microphone]",
      "\"jump desktop microphone\"",
      "[mac screen sharing microphone]",
      "\"mac screen sharing microphone\"",
      "[cloud mac microphone]",
      "\"cloud mac microphone\"",
    ],
    headlines: [
      "Mic Missing on Remote Mac",
      "Use Your Mic on Remote Mac",
      "Add Voice to Remote Access",
      "Remote Mac Mic Passthrough",
      "Real Mic for the Remote Mac",
      "Speak Into Your Remote Mac",
      "Your Browser Becomes the Mic",
      "Keep Your Setup, Add a Mic",
      "Mic for Mac Remote Sessions",
      "Voice for Cloud Mac Minis",
      "Real System Mic on Mac",
      "A Mic That Follows You",
      "Try RelayMic for 7 Days",
      "3 Macs With One License",
      "Get RelayMic Early Access",
    ],
    descriptions: [
      "Your streaming tool handles control. RelayMic sends your voice as a Mac input.",
      "Built for cloud Macs and Mac minis you drive from another machine entirely.",
      "Type a 6-digit code, speak, then choose RelayMic in dictation, calls or recording.",
      "48 kHz Opus stereo, direct when possible, and zero bytes of your audio stored.",
    ],
  },
  {
    name: "Voice Tasks",
    finalUrl: "https://relaymic.com/?ref=voice-tasks",
    keywords: [
      "[dictation on remote mac]",
      "\"dictation on remote mac\"",
      "[voice input remote mac]",
      "\"voice input remote mac\"",
      "[remote mac dictation microphone]",
      "\"remote mac dictation microphone\"",
      "[use phone as microphone for remote mac]",
      "\"use phone as microphone for remote mac\"",
      "[browser microphone to mac]",
      "\"browser microphone to mac\"",
      "[use browser as microphone mac]",
      "\"use browser as microphone mac\"",
      "[virtual microphone mac remote]",
      "\"virtual microphone mac remote\"",
      "[record on remote mac]",
      "\"record on remote mac\"",
    ],
    headlines: [
      "Dictate on a Remote Mac",
      "Record on a Remote Mac",
      "Take Calls on a Remote Mac",
      "Browser Mic for Remote Mac",
      "Phone Mic to a Remote Mac",
      "Voice Input for Remote Mac",
      "Turn Browser Into a Mac Mic",
      "Use Your Voice Across Mac Apps",
      "Real System Mic on Mac",
      "Speak Into Your Remote Mac",
      "Mic Input From Any Browser",
      "Remote Mac Audio Input",
      "Try RelayMic for 7 Days",
      "3 Macs With One License",
      "Get RelayMic Early Access",
    ],
    descriptions: [
      "Dictate, take calls or record on a remote Mac using the mic where you actually are.",
      "Open RelayMic in a browser, enter a 6-digit code and speak into the remote Mac.",
      "Every Mac app sees RelayMic in its input-device list like an ordinary microphone.",
      "For a Mac you control from elsewhere, not a phone sitting next to your own Mac.",
    ],
  },
  {
    name: "Espanol Mac Remoto",
    finalUrl: "https://relaymic.com/es/?ref=es",   // 西语组落到西语页
    keywords: [
      "[micrófono escritorio remoto mac]",
      "\"micrófono escritorio remoto mac\"",
      "[usar micrófono en escritorio remoto]",
      "\"usar micrófono en escritorio remoto\"",
      "[usar micrófono en escritorio remoto mac]",
      "\"usar micrófono en escritorio remoto mac\"",
      "[micrófono remoto mac]",
      "\"micrófono remoto mac\"",
      "[pasar micrófono por escritorio remoto]",
      "\"pasar micrófono por escritorio remoto\"",
      "[anydesk micrófono no funciona]",
      "\"anydesk micrófono no funciona\"",
      "[anydesk micrófono no funciona mac]",
      "\"anydesk micrófono no funciona mac\"",
      "[teamviewer micrófono mac]",
      "\"teamviewer micrófono mac\"",
      "[micrófono virtual mac]",
      "\"micrófono virtual mac\"",
      "[dictado por voz mac remoto]",
      "\"dictado por voz mac remoto\"",
      "[usar celular como micrófono para mac]",
      "\"usar celular como micrófono para mac\"",
      "[usar móvil como micrófono mac]",
      "\"usar móvil como micrófono mac\"",
    ],
    headlines: [
      "Micrófono para Mac remoto",
      "Habla en un Mac remoto",
      "Usa tu micrófono a distancia",
      "Audio para escritorio remoto",
      "Tu voz llega al Mac remoto",
      "Micrófono real en el Mac",
      "Voz para tu Mac a distancia",
      "Micrófono desde el navegador",
      "Habla desde cualquier equipo",
      "RelayMic para acceso remoto",
      "Dicta en un Mac remoto",
      "Graba en un Mac remoto",
      "Usa tu voz en apps del Mac",
      "Prueba RelayMic por 7 días",
      "Solicita acceso anticipado",
    ],
    descriptions: [
      "Envía tu voz desde cualquier navegador a un Mac remoto como micrófono real del sistema.",
      "El escritorio remoto lleva pantalla, teclado y ratón. RelayMic añade tu micrófono.",
      "Dicta, graba o haz llamadas en el Mac remoto con el micrófono que tienes contigo.",
      "Opus estéreo a 48 kHz, conexión directa si es posible y cero audio almacenado.",
    ],
  },
];
