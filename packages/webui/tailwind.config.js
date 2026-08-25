/** @type {import('tailwindcss').Config} */
/* 瓷梅刻印主题：颜色由 src/index.css 的语义变量驱动。 */
export default {
  darkMode: ['class'],
  content: ['./index.html', './src/**/*.{ts,tsx}'],
  theme: {
    extend: {
      // ≥761px 完整侧栏常驻；≤760px 侧栏完全隐藏（走移动抽屉）。
      screens: {
        rail: '761px',
      },
      colors: {
        // shadcn 语义色（值来自 index.css 的 HSL 变量）
        border: 'hsl(var(--border))',
        input: 'hsl(var(--input))',
        ring: 'hsl(var(--ring))',
        background: 'hsl(var(--background))',
        foreground: 'hsl(var(--foreground))',
        primary: {
          DEFAULT: 'hsl(var(--primary))',
          foreground: 'hsl(var(--primary-foreground))',
        },
        secondary: {
          DEFAULT: 'hsl(var(--secondary))',
          foreground: 'hsl(var(--secondary-foreground))',
        },
        destructive: {
          DEFAULT: 'hsl(var(--destructive))',
          foreground: 'hsl(var(--destructive-foreground))',
        },
        success: {
          DEFAULT: 'hsl(var(--success))',
          foreground: 'hsl(var(--success-foreground))',
        },
        muted: {
          DEFAULT: 'hsl(var(--muted))',
          foreground: 'hsl(var(--muted-foreground))',
        },
        accent: {
          DEFAULT: 'hsl(var(--accent))',
          foreground: 'hsl(var(--accent-foreground))',
        },
        popover: {
          DEFAULT: 'hsl(var(--popover))',
          foreground: 'hsl(var(--popover-foreground))',
        },
        card: {
          DEFAULT: 'hsl(var(--card))',
          foreground: 'hsl(var(--card-foreground))',
        },
        sidebar: {
          DEFAULT: 'hsl(var(--sidebar))',
          foreground: 'hsl(var(--sidebar-foreground))',
        },
        // 品牌 token（十六进制变量，随明暗主题切换）
        rose: { DEFAULT: 'var(--rose)', soft: 'var(--rose-soft)' },
        jade: 'var(--jade)',
        ember: 'var(--ember)',
        amber: 'var(--amber)',
        orchid: 'var(--orchid)',
        code: 'var(--code-bg)',
        wash: 'var(--wash)',
      },
      fontFamily: {
        sans: [
          '-apple-system', 'BlinkMacSystemFont', 'PingFang SC', 'Hiragino Sans GB',
          'Segoe UI', 'Microsoft YaHei', 'Helvetica Neue', 'sans-serif',
        ],
        display: [
          'Fraunces', 'Songti SC', 'SimSun', 'serif',
        ],
        mono: [
          'ui-monospace', 'SF Mono', 'Cascadia Mono', 'Menlo', 'Consolas', 'monospace',
        ],
      },
      fontSize: {
        '2xs': ['11px', { lineHeight: '1.5' }],
        xs: ['12px', { lineHeight: '1.6' }],
        sm: ['13px', { lineHeight: '1.6' }],
        base: ['14px', { lineHeight: '1.6' }],
        lg: ['16px', { lineHeight: '1.5' }],
        xl: ['19px', { lineHeight: '1.4' }],
        '2xl': ['30px', { lineHeight: '1.15' }],
        display: ['30px', { lineHeight: '1.25' }],
      },
      transitionTimingFunction: {
        // 抽屉、开关和展开行共用的平滑缓动。
        smooth: 'cubic-bezier(0.2, 0.8, 0.2, 1)',
      },
      borderRadius: {
        xl: 'calc(var(--radius) + 4px)',
        lg: 'var(--radius)',        // 0.45rem ≈ 7px
        md: 'calc(var(--radius) - 2px)',
        sm: 'calc(var(--radius) - 4px)',
      },
      boxShadow: {
        soft: 'var(--shadow-soft)',
        lg: 'var(--shadow-lg)',
        'pri-glow': 'inset 0 1px 0 var(--btn-inset-hi), 0 6px 18px -8px var(--halo-a)',
      },
      backgroundImage: {
        // 品牌渐变（KPI 数字、主按钮、进度条、导航菱形、立绘）
        'brand-grad': 'linear-gradient(135deg, var(--grad-a), var(--grad-b))',
        // 侧栏纵向渐变
        'rail-fade': 'linear-gradient(180deg, hsl(var(--secondary)), hsl(var(--background)) 240px)',
      },
      keyframes: {
        'fade-in': { from: { opacity: '0', transform: 'translateY(6px)' }, to: { opacity: '1', transform: 'translateY(0)' } },
        'toast-in': {
          from: { opacity: '0', transform: 'translateY(14px) scale(0.97)' },
          to: { opacity: '1', transform: 'translateY(0) scale(1)' },
        },
        'toast-out': {
          from: { opacity: '1', transform: 'translateY(0) scale(1)' },
          to: { opacity: '0', transform: 'translateY(8px) scale(0.98)' },
        },
        rise: { from: { opacity: '0', transform: 'translateY(8px)' } },
        shimmer: { '100%': { transform: 'translateX(100%)' } },
        breathe: {
          '50%': { boxShadow: '0 0 0 5px color-mix(in srgb, var(--jade) 8%, transparent)' },
        },
      },
      animation: {
        'fade-in': 'fade-in 0.3s ease-out',
        'toast-in': 'toast-in 0.32s cubic-bezier(0.2, 0.8, 0.2, 1)',
        'toast-out': 'toast-out 0.2s ease-in forwards',
        rise: 'rise 0.35s cubic-bezier(0.2, 0.8, 0.2, 1)',
        breathe: 'breathe 2.6s ease-in-out infinite',
      },
    },
  },
  plugins: [require('tailwindcss-animate')],
}
