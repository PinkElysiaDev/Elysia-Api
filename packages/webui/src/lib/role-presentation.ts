import mediaVersion from './login-media-version.json'

export const ROLE_ANCHOR_CLASS = 'pointer-events-none fixed right-[calc(100%_-_100vw_-_6px)] top-[14px] w-[280px] sm:w-[380px] md:w-[480px] lg:w-[560px] xl:w-[640px]'

export function roleMaskStyle() {
  const source = `${import.meta.env.BASE_URL}role-mask.png?v=${mediaVersion.target}`
  return {
    WebkitMaskImage: `url(${source})`, maskImage: `url(${source})`,
    WebkitMaskSize: 'contain', maskSize: 'contain', WebkitMaskRepeat: 'no-repeat', maskRepeat: 'no-repeat',
    WebkitMaskPosition: 'top right', maskPosition: 'top right',
  } as const
}
