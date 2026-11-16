export function sensitiveDownloadURL(value: string) {
  try {
    const url = new URL(value)
    if (url.username || url.password || /\/webhook/i.test(url.pathname) || url.hostname.toLowerCase()==='api.telegram.org' && /^\/bot/i.test(url.pathname) || url.hostname.toLowerCase()==='qyapi.weixin.qq.com') return true
    for (const original of url.searchParams.keys()) {
      let key=original
      for(let attempt=0;attempt<3;attempt++){try{const decoded=decodeURIComponent(key);if(decoded===key)break;key=decoded}catch{break}}
      key=key.toLowerCase().replace(/[_\-.\[\]]/g,'')
      if (/(token|secret|password|passwd|credential|signature|authorization|authentication|accesskey|apikey|privatekey|authkey|sessionid)/.test(key) || ['key','sig','auth','hdnts','hdntl','hdnea','gda','jwt','bearer','hmac','session'].includes(key)) return true
    }
  } catch {}
  return false
}

export function safeText(value?: string) {
  return (value || '').replace(/https?:\/\/[^\s<>"']+/gi,url=>sensitiveDownloadURL(url)?'[已脱敏链接]':url).replace(/\b(token|secret|password|signature|authorization|api[_-]?key)\s*[:=]\s*[^\s,;]+/gi,'$1=[已脱敏]')
}
