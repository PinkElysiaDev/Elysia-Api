// ElysiaApi 更新提示胶囊：左下角悬浮卡片，覆盖在 WebUI 之上而不挤压布局。
// 与旧版 56pt 底栏不同，胶囊只改变透明度与位移，WebUI 排版保持静止。
//
// 状态机由 AppDelegate 驱动（.checking/.available/.downloading/.installing/.failed/.readyToRelaunch），
// 本类只负责「给定 phase + 文案 + 进度，渲染一张卡片」；进出动画是弹簧曲线的平移+淡入。

import Cocoa

/// 环形进度：分隔线灰环作轨道，瑰梅红圆头弧线按 fraction 顺时针爬行。
/// 原生 NSProgressIndicator 没有环形确定进度样式，这里用 16 行 draw 替代。
final class RingProgressView: NSView {
    // 测试断言探针属性（与业务无关）：fraction 读取当前进度。
    var fraction: Double = 0 { didSet { needsDisplay = true } }
    var lineTint: NSColor = .systemBlue { didSet { needsDisplay = true } }

    override func draw(_ dirtyRect: NSRect) {
        let inset = bounds.insetBy(dx: 1.5, dy: 1.5)
        let radius = min(inset.width, inset.height) / 2
        let center = NSPoint(x: inset.midX, y: inset.midY)
        NSColor.separatorColor.withAlphaComponent(0.35).setStroke()
        NSBezierPath(ovalIn: NSRect(x: center.x - radius, y: center.y - radius,
                                    width: radius * 2, height: radius * 2)).stroke()
        guard fraction > 0 else { return }
        let arc = NSBezierPath()
        arc.appendArc(withCenter: center, radius: radius,
                      startAngle: 90, endAngle: 90 - CGFloat(360 * min(fraction, 1)), clockwise: true)
        arc.lineWidth = 2.5
        arc.lineCapStyle = .round
        lineTint.setStroke()
        arc.stroke()
    }
}

/// 更新提示胶囊。宽高固定、文字截断——不同 phase 不会撑出不同尺寸，进出只走弹簧动画。
final class UpdateCapsuleView: NSView {
    static let width: CGFloat = 344
    static let height: CGFloat = 72
    static let cornerRadius: CGFloat = 12

    /// 关闭按钮拉伸热区（视觉上 16×16，点击宽容到 24×24）
    var onPrimaryAction: (() -> Void)?
    var onCancelAction: (() -> Void)?
    var onDismiss: (() -> Void)?

    // 以下控件按语义命名并保留 internal 访问级，供 NativeTests 直接断言当前相位外观；
    // 生产代码只通过 configure(...) 修改它们。
    private let iconView = NSImageView()
    let ringView = RingProgressView()
    private let spinner = NSProgressIndicator()
    let titleField = NSTextField(labelWithString: "")
    let detailField = NSTextField(labelWithString: "")
    let primaryButton = NSButton(title: "", target: nil, action: nil)
    let cancelButton = NSButton(title: "", target: nil, action: nil)
    let dismissButton = NSButton(frame: .zero)
    private var entrancePlayed = false

    /// 瑰梅红主题记号（与 PulseMenuView 同一对色值，深浅菜单各自取色）
    private var tint: NSColor {
        let dark = effectiveAppearance.bestMatch(from: [.darkAqua, .aqua]) == .darkAqua
        return dark ? NSColor(srgbRed: 0.914, green: 0.388, blue: 0.596, alpha: 1)   // #E96398
                    : NSColor(srgbRed: 0.863, green: 0.094, blue: 0.403, alpha: 1)   // #DC185D
    }

    override init(frame frameRect: NSRect) {
        super.init(frame: frameRect)
        setup()
    }

    required init?(coder: NSCoder) { fatalError("init(coder:) is not supported") }

    override var intrinsicContentSize: NSSize { NSSize(width: Self.width, height: Self.height) }
    override var wantsUpdateLayer: Bool { true }

    private func setup() {
        wantsLayer = true
        layer?.cornerRadius = Self.cornerRadius
        // background/border 随 cornerRadius 圆角；子视图不截断——内容本就收在圆角内，
        // false 才能让阴影露出。
        layer?.masksToBounds = false

        iconView.translatesAutoresizingMaskIntoConstraints = false
        iconView.imageScaling = .scaleNone
        iconView.imageAlignment = .alignCenter

        ringView.wantsLayer = true
        ringView.translatesAutoresizingMaskIntoConstraints = false

        spinner.style = .spinning
        spinner.controlSize = .small
        spinner.isDisplayedWhenStopped = false
        spinner.translatesAutoresizingMaskIntoConstraints = false

        titleField.font = .systemFont(ofSize: 13, weight: .semibold)
        titleField.textColor = .labelColor
        titleField.lineBreakMode = .byTruncatingTail
        titleField.maximumNumberOfLines = 1
        titleField.translatesAutoresizingMaskIntoConstraints = false
        titleField.setContentHuggingPriority(.init(100), for: .horizontal)
        titleField.setContentCompressionResistancePriority(.init(100), for: .horizontal)

        detailField.font = .systemFont(ofSize: 11)
        detailField.textColor = .secondaryLabelColor
        detailField.lineBreakMode = .byTruncatingMiddle
        detailField.maximumNumberOfLines = 1
        detailField.translatesAutoresizingMaskIntoConstraints = false
        detailField.setContentHuggingPriority(.init(100), for: .horizontal)
        detailField.setContentCompressionResistancePriority(.init(100), for: .horizontal)

        let textColumn = NSStackView(views: [titleField, detailField])
        textColumn.orientation = .vertical
        textColumn.alignment = .leading
        textColumn.distribution = .fill
        textColumn.spacing = 2
        textColumn.translatesAutoresizingMaskIntoConstraints = false
        titleField.widthAnchor.constraint(equalTo: textColumn.widthAnchor).isActive = true
        detailField.widthAnchor.constraint(equalTo: textColumn.widthAnchor).isActive = true

        primaryButton.action = #selector(handlePrimary)
        cancelButton.action = #selector(handleCancel)
        for button in [primaryButton, cancelButton] {
            button.translatesAutoresizingMaskIntoConstraints = false
            button.bezelStyle = .rounded
            button.controlSize = .small
            button.target = self
        }
        // 常用语义：可用/失败/就绪与下载中不共存，同一槽位切换。
        cancelButton.title = "取消下载"

        let buttonRow = NSStackView(views: [primaryButton, cancelButton])
        buttonRow.orientation = .horizontal
        buttonRow.spacing = 8
        buttonRow.translatesAutoresizingMaskIntoConstraints = false
        primaryButton.setContentHuggingPriority(.required, for: .horizontal)
        cancelButton.setContentHuggingPriority(.required, for: .horizontal)
        primaryButton.setContentCompressionResistancePriority(.required, for: .horizontal)
        cancelButton.setContentCompressionResistancePriority(.required, for: .horizontal)

        dismissButton.bezelStyle = .inline
        dismissButton.isBordered = false
        dismissButton.title = ""
        dismissButton.image = NSImage(systemSymbolName: "xmark", accessibilityDescription: "关闭更新提示")?
            .withSymbolConfiguration(.init(pointSize: 9, weight: .semibold))
        dismissButton.contentTintColor = .tertiaryLabelColor
        dismissButton.target = self
        dismissButton.action = #selector(handleDismiss)
        dismissButton.translatesAutoresizingMaskIntoConstraints = false

        addSubview(iconView)
        addSubview(ringView)
        addSubview(spinner)
        addSubview(textColumn)
        addSubview(buttonRow)
        addSubview(dismissButton)

        NSLayoutConstraint.activate([
            iconView.leadingAnchor.constraint(equalTo: leadingAnchor, constant: 16),
            iconView.centerYAnchor.constraint(equalTo: centerYAnchor),
            iconView.widthAnchor.constraint(equalToConstant: 22),
            iconView.heightAnchor.constraint(equalToConstant: 22),

            ringView.centerXAnchor.constraint(equalTo: iconView.centerXAnchor),
            ringView.centerYAnchor.constraint(equalTo: iconView.centerYAnchor),
            ringView.widthAnchor.constraint(equalToConstant: 22),
            ringView.heightAnchor.constraint(equalToConstant: 22),

            spinner.centerXAnchor.constraint(equalTo: iconView.centerXAnchor),
            spinner.centerYAnchor.constraint(equalTo: iconView.centerYAnchor),

            textColumn.leadingAnchor.constraint(equalTo: iconView.trailingAnchor, constant: 10),
            textColumn.centerYAnchor.constraint(equalTo: centerYAnchor),
            textColumn.trailingAnchor.constraint(equalTo: buttonRow.leadingAnchor, constant: -10),

            buttonRow.trailingAnchor.constraint(equalTo: trailingAnchor, constant: -12),
            buttonRow.centerYAnchor.constraint(equalTo: centerYAnchor),

            // 关闭 × 钉在右上角，与操作按钮分行——不再挤在按钮右侧。
            // 18×18 命中区里 9pt 符号自动居中，视觉位置与原来一致，只是更好点。
            dismissButton.trailingAnchor.constraint(equalTo: trailingAnchor, constant: -8),
            dismissButton.topAnchor.constraint(equalTo: topAnchor, constant: 8),
            dismissButton.widthAnchor.constraint(equalToConstant: 18),
            dismissButton.heightAnchor.constraint(equalToConstant: 18),
        ])
    }

    /// 给定 phase 渲染内容与控件。visibleActions 描述当前阶段允许的操作，
    /// 胶囊不负责业务分支之外的语义（文案已由 AppDelegate 从同一状态机生成）。
    func configure(phase: UpdatePhase, title: String, detail: String, fraction: Double?) {
        titleField.stringValue = title
        detailField.stringValue = detail
        titleField.toolTip = title
        detailField.toolTip = detail

        primaryButton.isHidden = ![.available, .failed, .readyToRelaunch].contains(phase)
        cancelButton.isHidden = phase != .downloading
        dismissButton.isHidden = ![.available, .failed].contains(phase)

        switch phase {
        case .available:
            primaryButton.title = "立即更新"
            primaryButton.isEnabled = true
        case .failed:
            primaryButton.title = "重试"
            primaryButton.isEnabled = true
        case .readyToRelaunch:
            primaryButton.title = "重新启动"
            primaryButton.isEnabled = true
        case .downloading:
            cancelButton.isEnabled = true
        case .checking, .installing, .idle:
            break
        }

        // 图标槽：下载中=环形进度，安装中=小菊花，其余=语义符号。
        ringView.isHidden = phase != .downloading
        if phase == .downloading {
            ringView.lineTint = tint
            ringView.fraction = fraction ?? 0
        }
        if phase == .installing || phase == .checking { spinner.startAnimation(self) }
        else { spinner.stopAnimation(self) }

        iconView.isHidden = phase == .downloading || phase == .installing || phase == .checking
        if !iconView.isHidden {
            let (name, color): (String, NSColor) = {
                switch phase {
                case .available:       return ("arrow.down.circle.fill", tint)
                case .failed:          return ("exclamationmark.triangle.fill", .systemRed)
                case .readyToRelaunch: return ("checkmark.circle.fill", .systemGreen)
                default:               return ("arrow.down.circle.fill", tint)
                }
            }()
            iconView.image = NSImage(systemSymbolName: name, accessibilityDescription: title)?
                .withSymbolConfiguration(.init(pointSize: 21, weight: .medium))
            iconView.contentTintColor = color
        }
    }

    override func updateLayer() {
        layer?.backgroundColor = (window?.backgroundColor ?? .windowBackgroundColor).cgColor
        layer?.shadowColor = NSColor.black.cgColor
        layer?.shadowRadius = 14
        layer?.shadowOffset = NSSize(width: 0, height: -5)
        layer?.borderColor = cgBorder
        layer?.borderWidth = 0.5
    }

    override func viewDidChangeBackingProperties() {
        // 多屏/DPR 切换时阴影按物理像素锐化
        layer?.shadowRadius = 14
    }

    override func viewDidChangeEffectiveAppearance() {
        needsDisplay = true
    }

    private var cgBorder: CGColor {
        let dark = effectiveAppearance.bestMatch(from: [.darkAqua, .aqua]) == .darkAqua
        let base: NSColor = dark ? .white : .black
        return base.withAlphaComponent(dark ? 0.16 : 0.10).cgColor
    }

    /// 从下方轻滑入并轻微过冲：220ms 位移 + 180ms 淡入，阴影路径不变形。
    func animateEntrance() {
        guard !entrancePlayed, let layer else { return }
        entrancePlayed = true
        let move = CABasicAnimation(keyPath: "transform.translation.y")
        move.fromValue = 16
        move.toValue = 0
        move.duration = 0.24
        move.timingFunction = CAMediaTimingFunction(controlPoints: 0.34, 1.35, 0.64, 1)
        let fade = CABasicAnimation(keyPath: "opacity")
        fade.fromValue = 0
        fade.toValue = 1
        fade.duration = 0.18
        let group = CAAnimationGroup()
        group.animations = [move, fade]
        group.duration = 0.24
        group.isRemovedOnCompletion = true
        layer.add(group, forKey: "capsule-entrance")
    }

    /// 滑出隐藏：完成后回调由 AppDelegate 真正设 isHidden，避免动画末尾闪回。
    func animateExit(completion: @escaping () -> Void) {
        guard let layer else { isHidden = true; completion(); return }
        CATransaction.begin()
        CATransaction.setCompletionBlock { [weak self] in
            self?.isHidden = true
            completion()
        }
        let move = CABasicAnimation(keyPath: "transform.translation.y")
        move.fromValue = 0
        move.toValue = 16
        move.duration = 0.16
        move.timingFunction = CAMediaTimingFunction(name: .easeIn)
        let fade = CABasicAnimation(keyPath: "opacity")
        fade.fromValue = 1
        fade.toValue = 0
        fade.duration = 0.14
        let group = CAAnimationGroup()
        group.animations = [move, fade]
        group.duration = 0.16
        group.isRemovedOnCompletion = false
        layer.add(group, forKey: "capsule-exit")
        CATransaction.commit()
    }

    @objc private func handlePrimary() { onPrimaryAction?() }
    @objc private func handleCancel() { onCancelAction?() }
    @objc private func handleDismiss() { onDismiss?() }
}
