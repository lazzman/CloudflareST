import { describe, expect, it } from 'vitest'
import { firstSummaryChannels, retryConnections } from '../src/lib/notifications'
import type { Connection, Delivery } from '../src/api/types'

const connections: Connection[] = [
  { id: 'telegram-original', kind: 'telegram', credentials_set: true },
  { id: 'telegram-other', kind: 'telegram', credentials_set: true },
  { id: 'wecom-valid', kind: 'wecom', credentials_set: true },
  { id: 'wecom-empty', kind: 'wecom', credentials_set: false },
]
const failed: Delivery = { id: 'delivery-original', attempt_id: 'different-attempt', kind: 'telegram', connection_id: 'telegram-original', state: 'failed' }

describe('摘要渠道与原尝试范围', () => {
  it('从未投递时，即使自动通知关闭也可使用有效连接首次发送', () => {
    expect(firstSummaryChannels([], connections)).toEqual(['telegram', 'wecom'])
  })
  it.each(['success', 'pending', 'dispatching', 'running', 'accepted', 'failed', 'unconfirmed', 'unknown'])('已有 %s 投递的渠道不能再走首次发送', state => {
    expect(firstSummaryChannels([{ ...failed, state }], connections)).toEqual(['wecom'])
  })
  it('跳过的通知不阻止首次手动发送；无凭据连接不可用', () => {
    expect(firstSummaryChannels([{ kind: 'wecom', state: 'skipped' }], connections)).toEqual(['telegram', 'wecom'])
    expect(firstSummaryChannels([], connections.filter(item => item.kind !== 'wecom' || !item.credentials_set))).toEqual(['telegram'])
  })
  it('重试仅允许原连接，不能自动改绑同渠道其他连接', () => {
    expect(retryConnections(failed, [failed], connections).map(item => item.id)).toEqual(['telegram-original'])
    expect(retryConnections(failed, [failed], connections.filter(item => item.id !== 'telegram-original'))).toEqual([])
  })
  it.each(['success', 'pending', 'unknown'])('状态 %s 不提供重试候选', state => {
    expect(retryConnections({ ...failed, state }, [{ ...failed, state }], connections)).toEqual([])
  })
  it('后续尝试已存在时不能重复重试较早失败项', () => {
    expect(retryConnections(failed, [failed, { ...failed, id: 'newer', state: 'pending' }], connections)).toEqual([])
  })
  it('缺少原投递 ID 时不凭 attempt_id 猜测接口身份', () => {
    expect(retryConnections({ ...failed, id: undefined }, [failed], connections)).toEqual([])
  })
})
