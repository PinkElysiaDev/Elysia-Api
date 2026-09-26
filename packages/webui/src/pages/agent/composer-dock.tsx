import { ArrowUp, FileText, Plus, Square, X } from "lucide-react";
import { useRef } from "react";
import { Button } from "@/components/ui/button";
import { Textarea } from "@/components/ui/input";
import type { Model, ModelSource } from "@/lib/types";
import type { AgentDocument, AgentSettings } from "@/lib/agent/types";
import { cn } from "@/lib/utils";
import { ContextGauge, type SessionUsageStat } from "./context-gauge";
import { PermissionMenu, ThinkingMenu } from "./composer-menu";
import { ModelPicker } from "./model-picker";
import { isSendKeyEvent, sendKeyHint, useSendKeyMode } from "./send-key";

export interface ComposerDockProps {
  /** 输入框文本（状态由 ChatPanel 持有：草稿回写与发送都依赖它）。 */
  text: string;
  onTextChange: (text: string) => void;
  documents: AgentDocument[];
  setDocuments: React.Dispatch<React.SetStateAction<AgentDocument[]>>;
  addFiles: (files: FileList | File[]) => Promise<void>;
  busy: boolean;
  /** 未选模型时禁发并提示先选模型。 */
  needsModel: boolean;
  /** 设置保存中（显示“保存中…”提示）。 */
  saving: boolean;
  settings: AgentSettings;
  usage: SessionUsageStat | null;
  contextLimit: number;
  /** 编辑历史消息重发的在编消息（null 表示不在编辑态）。 */
  editingMessage: { seq: number; text: string } | null;
  onEditMessageChange: (message: { seq: number; text: string } | null) => void;
  /** 发送当前输入（Ctrl+Enter 与发送按钮共用）。 */
  onSubmit: () => void;
  onStop: () => void;
  onSend: (input: {
    content?: string;
    documents?: AgentDocument[];
    afterSeq?: number;
  }) => void;
  onSettingsSave: (patch: { settings?: Partial<AgentSettings> }) => void;
  onModelSelect: (source: ModelSource, model: Model) => void;
}

/**
 * ComposerDock：输入与全部会话设置一体的输入容器。默认有线无底（边框常显、
 * 内部透明），hover / 聚焦时填充浮现。底部控制条从左到右：
 * 附加 / 权限控制 / 会话统计 / 模型 / 思考强度 / 发送。
 */
export function ComposerDock({
  text,
  onTextChange,
  documents,
  setDocuments,
  addFiles,
  busy,
  needsModel,
  saving,
  settings,
  usage,
  contextLimit,
  editingMessage,
  onEditMessageChange,
  onSubmit,
  onStop,
  onSend,
  onSettingsSave,
  onModelSelect,
}: ComposerDockProps) {
  const fileInputRef = useRef<HTMLInputElement>(null);
  const sendMode = useSendKeyMode();

  /** 按发送键偏好提交主输入/编辑重发（IME 组词回车不触发）。 */
  const submitOnKey = (
    event: React.KeyboardEvent<HTMLTextAreaElement>,
    submit: () => void,
  ) => {
    if (!isSendKeyEvent(event, sendMode)) return;
    event.preventDefault();
    submit();
  };

  return (
    <div className="rounded-xl border border-border bg-transparent transition-colors duration-200 hover:bg-card focus-within:border-rose focus-within:bg-card focus-within:ring-[3px] focus-within:ring-wash">
      {editingMessage ? (
        <div className="px-3 pb-2 pt-2.5">
          <div className="mb-1.5 flex items-center gap-2 text-2xs text-muted-foreground">
            编辑历史消息并在这里重发（之后的消息将被替换）
            <button
              type="button"
              className="ml-auto rounded p-0.5 hover:text-foreground"
              onClick={() => onEditMessageChange(null)}
            >
              <X className="h-3.5 w-3.5" />
            </button>
          </div>
          <Textarea
            className="min-h-[60px] border-0 bg-transparent px-0 text-sm focus-visible:border-0 focus-visible:ring-0"
            value={editingMessage.text}
            onChange={(event) =>
              onEditMessageChange({
                ...editingMessage,
                text: event.target.value,
              })
            }
            onKeyDown={(event) =>
              submitOnKey(event, () => {
                if (busy || !editingMessage.text.trim()) return;
                onSend({
                  content: editingMessage.text,
                  afterSeq: editingMessage.seq - 1,
                });
                onEditMessageChange(null);
              })
            }
          />
          <div className="flex justify-end">
            <Button
              size="sm"
              className="h-7"
              disabled={busy || !editingMessage.text.trim()}
              onClick={() => {
                onSend({
                  content: editingMessage.text,
                  afterSeq: editingMessage.seq - 1,
                });
                onEditMessageChange(null);
              }}
            >
              从这里重发
            </Button>
          </div>
        </div>
      ) : null}

      {documents.length > 0 ? (
        <div className="flex flex-wrap gap-1.5 px-3 py-2">
          {documents.map((doc, index) => (
            <span
              key={index}
              className="inline-flex items-center gap-1 rounded-md bg-muted px-2 py-1 text-2xs"
            >
              <FileText className="h-3 w-3 text-muted-foreground" />
              {doc.name ?? `材料 ${index + 1}`}
              <button
                type="button"
                aria-label={`移除附件 ${doc.name ?? index + 1}`}
                className="rounded p-0.5 hover:text-ember"
                onClick={() =>
                  setDocuments((current) =>
                    current.filter((_, i) => i !== index),
                  )
                }
              >
                <X className="h-3 w-3" />
              </button>
            </span>
          ))}
        </div>
      ) : null}

      <input
        ref={fileInputRef}
        type="file"
        multiple
        hidden
        onChange={(event) => {
          if (event.target.files?.length) void addFiles(event.target.files);
          event.target.value = "";
        }}
      />
      <Textarea
        className="max-h-56 min-h-[44px] w-full resize-none border-0 bg-transparent px-3.5 py-2.5 text-sm focus-visible:border-0 focus-visible:ring-0"
        placeholder={needsModel ? "先在下方选择模型…" : "请描述你的任务"}
        value={text}
        disabled={busy}
        onChange={(event) => onTextChange(event.target.value)}
        onKeyDown={(event) => submitOnKey(event, onSubmit)}
        onPaste={(event) => {
          const files = Array.from(event.clipboardData.files ?? []);
          if (files.length > 0) {
            event.preventDefault();
            void addFiles(files);
          }
        }}
      />

      {/* 底部控制条：左侧 附加/权限；右侧 模型/思考/上下文占用/发送。 */}
      <div className="flex flex-wrap items-center gap-2 px-2.5 py-2">
        <Button
          variant="ghost"
          size="icon"
          className="h-7 w-7 shrink-0 rounded-full border border-input"
          title="添加附件（文档 / 图片 / PDF）"
          disabled={busy}
          onClick={() => fileInputRef.current?.click()}
        >
          <Plus className="h-4 w-4" />
        </Button>
        <PermissionMenu
          settings={settings}
          disabled={busy}
          onChange={(patch) => void onSettingsSave(patch)}
        />
        <span
          className={cn(
            "text-2xs text-muted-foreground transition-opacity",
            saving ? "opacity-100" : "opacity-0",
          )}
        >
          保存中…
        </span>
        <div className="ml-auto flex items-center gap-2">
          <ContextGauge usage={usage} contextLimit={contextLimit} />
          <ModelPicker
            sourceId={settings.modelSourceId}
            modelName={settings.modelName}
            disabled={busy}
            onSelect={onModelSelect}
          />
          <ThinkingMenu
            settings={settings}
            disabled={busy}
            onChange={(patch) => void onSettingsSave(patch)}
          />
          {busy ? (
            <Button
              variant="ghost"
              size="icon"
              className="h-8 w-8 shrink-0 rounded-full text-muted-foreground transition-colors hover:bg-destructive hover:text-white"
              title="停止本轮"
              onClick={onStop}
            >
              <Square className="h-3.5 w-3.5" />
            </Button>
          ) : (
            <Button
              variant="ghost"
              size="icon"
              className="h-8 w-8 shrink-0 rounded-full text-muted-foreground transition-colors hover:bg-primary hover:text-primary-foreground"
              title={
                needsModel
                  ? "请先选择模型"
                  : `发送（${sendKeyHint(sendMode)}；Shift+Enter 换行）`
              }
              disabled={needsModel || (!text.trim() && documents.length === 0)}
              onClick={onSubmit}
            >
              <ArrowUp className="h-4 w-4" />
            </Button>
          )}
        </div>
      </div>
    </div>
  );
}
