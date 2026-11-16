import {describe,expect,it} from 'vitest'
import {sensitiveDownloadURL,safeText} from '../src/lib/safeText'
import {DEFAULT_CONFIG,validateTask} from '../src/lib/domain'

describe('敏感下载地址与运行原因',()=>{
  it.each(['token','X-Amz-Signature','api_key','%2574oken','sig','access_token','auth_key','hdnts','hdnea'])('拒绝认证参数%s且原因不回显值',key=>{
    const url='https://example.invalid/file?'+key+'=FAKE_PRIVATE_VALUE'
    expect(sensitiveDownloadURL(url)).toBe(true)
    expect(safeText('下载失败 '+url)).not.toContain('FAKE_PRIVATE_VALUE')
    expect(validateTask({id:'',name:'任务',config:{...DEFAULT_CONFIG,download_url:url}}).download_url).toBeTruthy()
  })
  it('保留普通URL查询，不篡改合法文件地址',()=>{
    const url='https://example.invalid/file?bytes=1000&format=binary'
    expect(sensitiveDownloadURL(url)).toBe(false)
    expect(safeText(url)).toBe(url)
    expect(validateTask({id:'',name:'任务',config:{...DEFAULT_CONFIG,download_url:url}}).download_url).toBeUndefined()
  })
})
