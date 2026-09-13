import { useEffect, useRef, useState, type FormEvent } from 'react'
import { Eye, EyeOff, Loader2 } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { BrandMark } from '@/components/brand-mark'
import { RoleWatermark } from '@/components/role-watermark'
import { useTheme } from '@/lib/theme'
import { setToken } from '@/lib/auth'
import { verifyToken } from '@/lib/api'

/** 登录专用下划线令牌输入：一条输入线，聚焦时粉线自左向右展开。 */
function TokenLineInput({
  value,
  onChange,
}: {
  value: string
  onChange: (next: string) => void
}) {
  const [focused, setFocused] = useState(false)
  const [visible, setVisible] = useState(false)
  return (
    <div className="relative">
      <input
        type={visible ? 'text' : 'password'}
        aria-label="Panel Access Token"
        autoFocus
        value={value}
        placeholder="请输入访问令牌"
        onFocus={() => setFocused(true)}
        onBlur={() => setFocused(false)}
        onChange={(event) => onChange(event.target.value)}
        className="h-10 w-full rounded-none border-0 border-b border-input bg-transparent px-0 pb-2 pr-8 text-sm text-foreground transition-colors placeholder:text-muted-foreground focus:outline-none"
      />
      <span
        aria-hidden
        className={`pointer-events-none absolute bottom-0 left-0 h-[2px] w-full origin-left bg-primary transition-transform duration-300 ease-out ${
          focused ? 'scale-x-100' : 'scale-x-0'
        }`}
      />
      <button
        type="button"
        tabIndex={-1}
        onClick={() => setVisible((v) => !v)}
        className="absolute right-0 top-1/2 -translate-y-1/2 rounded-md p-1 text-muted-foreground transition-colors hover:text-rose"
        aria-label={visible ? '隐藏' : '显示'}
      >
        {visible ? <EyeOff className="h-4 w-4" /> : <Eye className="h-4 w-4" />}
      </button>
    </div>
  )
}

/** 刻印式主题切换：环 + 单色 logo，色彩表状态——日=琥珀，夜=月蓝。 */
function SealThemeToggle() {
  const { theme, toggleTheme } = useTheme()
  const dark = theme === 'dark'
  return (
    <button
      type="button"
      onClick={toggleTheme}
      aria-label={dark ? '切换到浅色模式' : '切换到深色模式'}
      aria-pressed={dark}
      title={dark ? '浅色模式' : '深色模式'}
      className={`inline-flex h-10 w-10 rotate-[-8deg] items-center justify-center rounded-full border transition-all duration-500 hover:brightness-125 ${
        dark
          ? 'border-orchid shadow-[0_0_12px_color-mix(in_srgb,var(--orchid)_35%,transparent)]'
          : 'border-amber-500/70 shadow-[0_0_12px_rgba(245,158,11,0.35)]'
      }`}
    >
      <img
        src={`${import.meta.env.BASE_URL}logo.png`}
        alt=""
        className="h-5 w-5 opacity-80"
      />
    </button>
  )
}

/** 悬停粒子的主页布点（偏下）与双频谐和轨道参数。 */
interface OrbitParticle {
  left: string
  top: string
  size: number
  delay: number
  tint: boolean
  /** 两轴各两个不呈整数比的角频率（rad/s）——准周期轨迹永不重复 */
  wx: [number, number]
  wy: [number, number]
  /** 谐和振幅（px），合计即轨道半径 */
  ax: [number, number]
  ay: [number, number]
}

const PARTICLE_HOMES: { left: string; top: string; size: number; delay: number; tint?: boolean }[] = [
  { left: '7%', top: '45%', size: 3, delay: 0 },
  { left: '13%', top: '90%', size: 2, delay: 90, tint: true },
  { left: '21%', top: '25%', size: 4, delay: 40 },
  { left: '31%', top: '95%', size: 2, delay: 140 },
  { left: '40%', top: '35%', size: 3, delay: 20, tint: true },
  { left: '49%', top: '75%', size: 2, delay: 110 },
  { left: '58%', top: '30%', size: 4, delay: 70 },
  { left: '67%', top: '85%', size: 3, delay: 160, tint: true },
  { left: '76%', top: '40%', size: 2, delay: 50 },
  { left: '84%', top: '70%', size: 3, delay: 130 },
  { left: '91%', top: '95%', size: 2, delay: 90, tint: true },
  { left: '96%', top: '35%', size: 4, delay: 30 },
]

// 互不成整数比的角频率候选（rad/s，整体放缓），随机配对出"多体式"不可解
// 观感；各粒子轨道参数独立，大振幅下自然相互交叉。
const OMEGAS = [0.35, 0.5, 0.65, 0.8, 1.0, 1.2, 1.5] as const

function makeOrbits(): OrbitParticle[] {
  const pick = (exclude?: number) => {
    let value = OMEGAS[Math.floor(Math.random() * OMEGAS.length)]
    while (value === exclude) value = OMEGAS[Math.floor(Math.random() * OMEGAS.length)]
    return value
  }
  return PARTICLE_HOMES.map((home) => {
    const wx1 = pick()
    const wy1 = pick()
    return {
      ...home,
      tint: home.tint ?? false,
      wx: [wx1, pick(wx1)],
      wy: [wy1, pick(wy1)],
      ax: [9 + Math.random() * 9, 4 + Math.random() * 5],
      ay: [13 + Math.random() * 13, 5 + Math.random() * 6],
    }
  })
}

/** 混沌轨道粒子：每颗沿自己的双频谐和轨道游走（准周期、不重复）；
 *  再次悬停时轨道时间归零，粒子从原位起跳并继续运动。 */
function ParticleOrbit({ loading }: { loading: boolean }) {
  const [orbits] = useState(makeOrbits)
  const [hovered, setHovered] = useState(false)
  const layerRef = useRef<HTMLDivElement>(null)
  const timeRef = useRef(0)

  useEffect(() => {
    if (!hovered) return
    timeRef.current = 0 // sin(0)=0：每次进入都恰好在原位
    let last = performance.now()
    let raf = 0
    const tick = (now: number) => {
      timeRef.current += (now - last) / 1000
      last = now
      const t = timeRef.current
      const dots = layerRef.current?.children
      if (dots) {
        for (let i = 0; i < dots.length; i += 1) {
          const orbit = orbits[i]
          const x = Math.sin(orbit.wx[0] * t) * orbit.ax[0] + Math.sin(orbit.wx[1] * t) * orbit.ax[1]
          const y = Math.sin(orbit.wy[0] * t) * orbit.ay[0] + Math.sin(orbit.wy[1] * t) * orbit.ay[1]
          ;(dots[i] as HTMLElement).style.transform = `translate(${x.toFixed(2)}px, ${y.toFixed(2)}px)`
        }
      }
      raf = requestAnimationFrame(tick)
    }
    raf = requestAnimationFrame(tick)
    return () => cancelAnimationFrame(raf)
  }, [hovered, orbits])

  return (
    <div
      className="relative"
      onPointerEnter={() => setHovered(true)}
      onPointerLeave={() => setHovered(false)}
    >
      <div ref={layerRef} aria-hidden className="pointer-events-none absolute inset-0">
        {orbits.map((particle, index) => (
          <span
            key={index}
            className={`absolute rounded-full transition-opacity duration-500 ease-out ${
              particle.tint ? 'bg-rose-soft' : 'bg-primary'
            } ${hovered ? 'opacity-80' : 'opacity-0'}`}
            style={{
              left: particle.left,
              top: particle.top,
              width: particle.size,
              height: particle.size,
              transitionDelay: hovered ? `${particle.delay}ms` : '0ms',
            }}
          />
        ))}
      </div>
      <Button
        type="submit"
        variant="ghost"
        className="relative h-10 w-full text-sm font-semibold text-foreground hover:bg-transparent hover:text-rose"
        disabled={loading}
      >
        {loading && <Loader2 className="h-4 w-4 animate-spin" />}
        {loading ? '身份验证中…' : '立即登录'}
      </Button>
    </div>
  )
}

export function LoginPage() {
  const [value, setValue] = useState('')
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)

  async function handleSubmit(event: FormEvent) {
    event.preventDefault()
    const token = value.trim()
    if (!token) {
      setError('请输入 Panel Access Token')
      return
    }
    setLoading(true)
    setError(null)
    try {
      const valid = await verifyToken(token)
      if (!valid) {
        setError('Token 无效，请检查服务端配置')
        return
      }
      setToken(token)
    } catch (err) {
      setError((err as Error).message || '无法连接到后端')
    } finally {
      setLoading(false)
    }
  }

  return (
    <div className="relative mx-auto grid min-h-screen w-full max-w-[1600px] place-items-center px-4">
      <RoleWatermark className="role-breathe opacity-20 dark:opacity-25 -right-4 top-1/2 -translate-y-1/2 rail:-right-8" />

      {/* 视口左下角：fixed 定位，超宽屏不受居中容器限制 */}
      <div className="fixed bottom-[22px] left-[22px] z-[60] max-rail:bottom-[14px] max-rail:left-[14px]">
        <SealThemeToggle />
      </div>

      <div className="relative z-[1] w-full max-w-[400px]">
        <BrandMark size="login" className="mb-7" />

        <div className="relative rounded-xl border border-border/80 bg-card/60 p-8 shadow-lg backdrop-blur-sm">
          <h1 className="font-display text-lg font-semibold tracking-tight text-foreground">Panel Access Token</h1>

          <form onSubmit={handleSubmit} className="mt-6 space-y-4">
            <div className="space-y-2">
              <TokenLineInput
                value={value}
                onChange={(next) => {
                  setValue(next)
                  setError(null)
                }}
              />
              {error && (
                <div className="rounded-lg bg-[color-mix(in_srgb,var(--ember)_10%,transparent)] px-3 py-2 text-xs font-medium text-ember">
                  {error}
                </div>
              )}
            </div>
            <ParticleOrbit loading={loading} />
          </form>
        </div>
      </div>
    </div>
  )
}
