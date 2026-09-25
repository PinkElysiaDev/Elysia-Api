// 消息内图片：气泡内轻量缩略网格（扁平、非卡片），点击进全屏灯箱放大。
import { useState } from "react";
import { LightboxStage } from "@/components/lightbox-stage";
import { isImageDocument, type AgentDocument } from "@/lib/agent/types";
import { cn } from "@/lib/utils";

/** 气泡内图片缩略网格 + 灯箱。无图片附件时渲染 null。 */
export function MessageImages({ documents }: { documents: AgentDocument[] }) {
  const images = documents.filter(isImageDocument);
  const [preview, setPreview] = useState<number | null>(null);
  if (images.length === 0) return null;
  const current = preview != null ? images[preview] : null;
  return (
    <>
      {/* 单图保比例展示（截长图不裁切）；多图方格裁切缩略、点击看原图。
          多图网格限宽，避免宽气泡把方格拉到失去「缩略」意义。
          dataUrl 由浏览器复用解码，lazy 限渲染时机即可。 */}
      <div
        className={cn(
          "grid gap-1.5",
          images.length === 1
            ? "grid-cols-1"
            : "max-w-[300px] " + (images.length <= 4 ? "grid-cols-2" : "grid-cols-3"),
        )}
      >
        {images.map((doc, index) => (
          <button
            key={index}
            type="button"
            onClick={() => setPreview(index)}
            aria-label={`放大查看图片 ${doc.name ?? index + 1}`}
            className="cursor-zoom-in overflow-hidden rounded-lg border border-border/70 bg-card/40 transition-colors hover:border-rose/50 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
          >
            <img
              src={doc.dataUrl}
              alt={doc.name ?? `图片 ${index + 1}`}
              loading="lazy"
              decoding="async"
              draggable={false}
              className={cn(
                "w-full object-cover",
                images.length === 1 ? "max-h-56 object-contain" : "aspect-square",
              )}
            />
          </button>
        ))}
      </div>
      <LightboxStage
        open={preview != null}
        src={current?.dataUrl ?? null}
        name={current?.name ?? `图片 ${(preview ?? 0) + 1}`}
        index={preview}
        total={images.length}
        onNavigate={setPreview}
        onClose={() => setPreview(null)}
      />
    </>
  );
}
