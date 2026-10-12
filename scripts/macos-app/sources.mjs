import { join } from 'node:path'

/** Production Swift sources shared by app builds and the native test executable. */
export function nativeAppSources(directory) {
  return [
    'MacSupport.swift', 'PanelBridge.swift', 'UpdateCapsuleView.swift',
    'BackendController.swift', 'PanelProcessController.swift',
    'PanelDownloadController.swift', 'UpdateController.swift', 'main.swift',
  ].map((name) => join(directory, name))
}
