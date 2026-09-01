import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";

const source = (path: string) => readFileSync(new URL(path, import.meta.url), "utf8");

test("the writing workspace keeps the document ahead of conversation and details", () => {
  const page = source("../src/pages/writing-workspace.tsx");
  const documentPosition = page.indexOf("<DocumentSurface");
  const conversationPosition = page.indexOf("<Thread variant=\"flow\"");
  const sidecarPosition = page.indexOf("<aside className=\"workspace-sidecar\"");
  assert.ok(documentPosition > 0);
  assert.ok(conversationPosition > documentPosition);
  assert.ok(sidecarPosition > conversationPosition);
  assert.match(page, /<Sidebar/);
  assert.match(page, /<Sidebar[\s\S]*onClose=/);
  assert.doesNotMatch(page, /globalSidebar|setGlobalSidebar|data-sidebar-state/);
  assert.doesNotMatch(page, /<RunSummaryStrip/);
  assert.match(page, /<DetailPanel governed/);
  assert.match(page, /workspace-sidecar/);
  assert.match(page, /data-panel-state=\{detailPanel\}/);
  assert.match(page, /data-detail-state=\{detailPanel\}/);
  assert.match(page, /workspace-composer-layer/);
  assert.match(page, /workspace-composer-layer-compact/);
  assert.doesNotMatch(page, /ConversationDock|写作对话/);
  assert.doesNotMatch(page, /workspace-sidecar-composer/);
  assert.doesNotMatch(page, /WRITING WORKSPACE/);
  assert.doesNotMatch(page, /workspace-balance/);
});

test("global navigation is a full drawer that can close at every width", () => {
  const page = source("../src/pages/writing-workspace.tsx");
  const sidebar = source("../src/components/sidebar/sidebar.tsx");
  const styles = source("../src/index.css");
  assert.match(sidebar, /w-64/);
  assert.match(sidebar, /ml-auto flex h-7 w-7/);
  assert.match(sidebar, /aria-label="关闭侧栏"/);
  assert.match(page, /useState\(\(\) => typeof window === "undefined" \|\| window\.innerWidth >= 1024\)/);
  assert.match(page, /onToggleSidebar: \(\) => setSidebarOpen/);
  assert.match(page, /data-sidebar-open=\{sidebarOpen\}/);
  assert.match(page, /workspace-scrim lg:hidden/);
  assert.match(page, /!sidebarOpen && <button[\s\S]*aria-label="打开全局导航"/);
  assert.doesNotMatch(sidebar, /w-14|PanelLeftOpen|展开侧栏|收起侧栏/);
  assert.doesNotMatch(styles, /data-sidebar-state="collapsed"|--workspace-sidebar-width: 56px/);
  assert.match(styles, /\.workspace-global-sidebar \{[\s\S]*transform: translateX\(-100%\)/);
  assert.match(styles, /\.workspace-global-sidebar-open \{[^}]*transform: translateX\(0\)/);
  assert.match(styles, /data-sidebar-open="true"[^}]*--workspace-sidebar-width: 256px/);
  assert.match(styles, /@media \(max-width: 1023px\)[\s\S]*data-sidebar-open[^}]*--workspace-sidebar-width: 0px/);
  assert.match(styles, /@media \(max-width: 767px\)[\s\S]*\.workspace-detail/);
});

test("the floating composer stays below both sidebars and yields to an open detail panel", () => {
  const styles = source("../src/index.css");
  assert.match(styles, /\.workspace-global-sidebar \{[^}]*z-index: 40/);
  assert.match(styles, /\.workspace-sidecar \{[^}]*z-index: 20/);
  assert.match(styles, /\.workspace-composer-layer \{[^}]*z-index: 15/);
  assert.match(styles, /data-detail-state="expanded"[^}]*--workspace-detail-space: var\(--workspace-detail-width\)/);
  assert.match(styles, /\.workspace-composer-layer \{[^}]*right: calc\(var\(--workspace-detail-space\) \+ 16px\)/);
  assert.match(styles, /\.governed-detail-panel \{[^}]*border-left: 1px solid hsl\(var\(--border\)\)/);
});

test("the document uses one continuous A4-backed sheet without guide rules", () => {
  const document = source("../src/components/document/document-surface.tsx");
  const styles = source("../src/index.css");
  assert.match(document, /document-paper-content/);
  assert.match(styles, /padding-top: 141\.4286%/);
  assert.doesNotMatch(styles, /document-masthead \{[^}]*border-bottom/);
  assert.doesNotMatch(styles, /document-paper::before \{[^}]*background:/);
});

test("the empty workspace is a welcome surface and paper starts with the first draft", () => {
  const document = source("../src/components/document/document-surface.tsx");
  const styles = source("../src/index.css");
  assert.match(document, /const hasDraft = Boolean\(version \|\| legacyDraft\?\.trim\(\)\)/);
  assert.match(document, /hasDraft \? <article className="document-paper">/);
  assert.match(document, /<section className="document-welcome"/);
  assert.ok(document.indexOf("document-welcome") > document.indexOf("</article>"));
  assert.doesNotMatch(styles, /\.document-welcome \{[^}]*border:/);
  assert.doesNotMatch(styles, /\.document-welcome \{[^}]*box-shadow:/);
});

test("agent process flows before the manuscript paper while the composer floats independently", () => {
  const page = source("../src/pages/writing-workspace.tsx");
  const document = source("../src/components/document/document-surface.tsx");
  const thread = source("../src/components/assistant-ui/thread.tsx");
  const threadPosition = page.indexOf("<Thread variant=\"flow\"");
  const composerPosition = page.indexOf("        <WritingComposer\n");
  const flowPosition = document.indexOf("document-conversation-flow");
  const paperPosition = document.indexOf("<article className=\"document-paper\"");
  assert.ok(threadPosition > 0);
  assert.ok(composerPosition > threadPosition);
  assert.ok(flowPosition > 0);
  assert.ok(paperPosition > flowPosition);
  assert.doesNotMatch(page, /ConversationDock|写作对话/);
  assert.match(page, /floating/);
  assert.match(page, /onToggleWidth/);
  assert.match(thread, /suppressArticle=\{variant !== "full"\}/);
});

test("user requests and agent process are independent flow items", () => {
  const user = source("../src/components/assistant-ui/user-message.tsx");
  const assistant = source("../src/components/assistant-ui/assistant-message.tsx");
  const timeline = source("../src/components/tools/compact-step-timeline.tsx");
  const layout = source("../src/stores/workspace-layout-store.ts");
  assert.match(user, /user-message-row/);
  assert.match(user, /user-message-bubble/);
  assert.match(assistant, /assistant-process-message/);
  assert.match(assistant, /<Collapsible open=\{open\} onOpenChange=\{setOpen\}>/);
  assert.doesNotMatch(assistant, /if \(isRunning && !userToggled\)/);
  assert.match(timeline, /useState\(defaultOpen\)/);
  assert.doesNotMatch(layout, /ConversationPanelState|conversationPanel|setConversationPanel/);
});

test("the document workspace shares the toolbar surface without a seam", () => {
  const styles = source("../src/index.css");
  assert.match(styles, /\.document-stage \{[^}]*background: hsl\(var\(--surface\)\)/);
  assert.doesNotMatch(styles, /\.workspace-toolbar \{[^}]*border-bottom/);
});

test("article feedback is rendered immediately below the manuscript paper", () => {
  const page = source("../src/pages/writing-workspace.tsx");
  const document = source("../src/components/document/document-surface.tsx");
  const assistant = source("../src/components/assistant-ui/assistant-message.tsx");
  assert.match(page, /afterPaper=\{feedbackContext/);
  assert.ok(document.indexOf("document-feedback") > document.indexOf("</article>"));
  assert.match(assistant, /!suppressArticle \|\| p\.dataType !== "feedback"/);
});

test("composer width control lives inside the composer and only reveals on interaction", () => {
  const composer = source("../src/components/composer/writing-composer.tsx");
  const page = source("../src/pages/writing-workspace.tsx");
  const styles = source("../src/index.css");
  assert.match(composer, /composer-width-toggle/);
  assert.match(composer, /收窄输入框/);
  assert.match(composer, /展开输入框/);
  assert.doesNotMatch(page, /workspace-composer-resize/);
  assert.match(styles, /\.composer-shell:hover \.composer-width-toggle/);
  assert.match(styles, /\.composer-width-toggle:focus-visible/);
  assert.match(styles, /\.composer-width-toggle \{[^}]*opacity: 0;[^}]*pointer-events: none/);
  assert.match(composer, /relative z-20 pb-4 pt-2/);
  assert.match(composer, /composer-control-row flex items-center py-2\.5/);
  assert.match(composer, /composer-width-icon/);
  assert.match(styles, /workspace-composer-layer-compact[^}]*left: max\(/);
  assert.match(styles, /@keyframes composer-width-icon-in/);
});

test("workspace drawers share one restrained motion system", () => {
  const page = source("../src/pages/writing-workspace.tsx");
  const styles = source("../src/index.css");
  assert.match(styles, /--workspace-motion-duration: 280ms/);
  assert.match(styles, /--workspace-motion-ease: cubic-bezier\(\.25, 1, \.5, 1\)/);
  assert.match(styles, /\.workspace-global-sidebar \{[\s\S]*transition: transform var\(--workspace-motion-duration\)/);
  assert.match(styles, /\.workspace-center \{[\s\S]*transition: margin-left var\(--workspace-motion-duration\)/);
  assert.match(styles, /\.workspace-sidecar \{[\s\S]*transition: width var\(--workspace-motion-duration\)/);
  assert.match(styles, /\.workspace-detail \{[\s\S]*transition: transform var\(--workspace-motion-duration\)/);
  assert.match(styles, /\.workspace-composer-layer \{[\s\S]*transition: left var\(--composer-motion-duration\)/);
  assert.match(styles, /prefers-reduced-motion: reduce/);
  assert.match(page, /aria-hidden=\{detailPanel === "collapsed"\}/);
  assert.doesNotMatch(page, /detailPanel !== "collapsed" &&/);
});

test("composer materials use drag upload, a floating menu, and a separate knowledge dialog", () => {
  const composer = source("../src/components/composer/writing-composer.tsx");
  const knowledgeDialog = source("../src/components/composer/knowledge-material-dialog.tsx");
  const model = source("../src/components/composer/model-picker.tsx");
  const styles = source("../src/index.css");
  assert.match(composer, /interface ComposerMaterial[\s\S]*title: string;[\s\S]*excerpt: string;/);
  assert.match(composer, /onDragEnter=\{handleDragEnter\}/);
  assert.match(composer, /onDrop=\{handleDrop\}/);
  assert.match(composer, /composer-drop-overlay/);
  assert.match(composer, /<Popover open=\{materialMenuOpen\}/);
  assert.match(composer, /composer-material-popover/);
  assert.match(composer, /accept="image\/\*" multiple/);
  assert.match(composer, /<KnowledgeMaterialDialog/);
  assert.match(composer, /uploadMaterial\(file, file\.name\)/);
  assert.doesNotMatch(composer, /composer-material-tray|composer-knowledge-picker/);
  assert.match(knowledgeDialog, /<DialogContent/);
  assert.match(knowledgeDialog, /搜索素材库资料/);
  assert.match(knowledgeDialog, /<Checkbox/);
  assert.match(knowledgeDialog, /添加所选素材/);
  assert.match(knowledgeDialog, /<Switch checked=\{kbEnabled\}/);
  assert.match(composer, /composer-material-library-row/);
  assert.match(composer, /<strong>素材库<\/strong>/);
  assert.doesNotMatch(composer, /composer-material-auto-search/);
  assert.match(composer, /const \[pendingKbEnabled, setPendingKbEnabled\] = useState\(true\)/);
  assert.match(styles, /\.composer-drop-overlay/);
  assert.match(styles, /\.composer-material-popover/);
  assert.match(styles, /@container \(max-width: 620px\)[\s\S]*\.composer-control-label/);
  assert.match(model, /return "快速"/);
  assert.match(model, /return "Pro"/);
  assert.match(model, /composer-model-speed/);
  assert.match(model, /composer-model-cost/);
});

test("the desktop detail panel has a bounded keyboard-accessible resize handle", () => {
  const page = source("../src/pages/writing-workspace.tsx");
  const styles = source("../src/index.css");
  assert.match(page, /DETAIL_WIDTH_MIN = 320/);
  assert.match(page, /DETAIL_WIDTH_MAX = 520/);
  assert.match(page, /DETAIL_RESIZE_VIEWPORT_MIN = 1180/);
  assert.match(page, /DOCUMENT_SAFE_WIDTH = 560/);
  assert.match(page, /role="separator"/);
  assert.match(page, /onPointerMove=\{resizeDetailFromPointer\}/);
  assert.match(page, /onKeyDown=\{resizeDetailFromKeyboard\}/);
  assert.match(styles, /@media \(min-width: 1180px\) and \(pointer: fine\)/);
  assert.match(styles, /cursor: col-resize/);
});

test("the detail surface exposes the five governed document tabs", () => {
  const detail = source("../src/components/runtime/run-detail-tabs.tsx");
  for (const tab of ["outline", "materials", "run", "quality", "versions"]) {
    assert.match(detail, new RegExp(`value: "${tab}"`));
  }
});

test("detail chrome is compact, icon-only, and spaced", () => {
  const page = source("../src/pages/writing-workspace.tsx");
  const panel = source("../src/components/sidebar/detail-panel.tsx");
  const styles = source("../src/index.css");
  const governedPanel = panel.slice(panel.indexOf("function GovernedDetailPanel"));
  assert.doesNotMatch(governedPanel, /DOCUMENT DESK|>收起<|ChevronRight/);
  assert.match(governedPanel, /PanelRightClose/);
  assert.match(page, /size="icon" aria-label="打开详情面板"/);
  assert.match(styles, /\.governed-detail-header \{[^}]*height: 52px/);
  assert.match(styles, /\.run-detail-tablist \{[^}]*gap: 4px/);
});

test("runtime progress never forces open legacy timelines or layout panels", () => {
  const timeline = source("../src/components/tools/compact-step-timeline.tsx");
  const page = source("../src/pages/writing-workspace.tsx");
  assert.doesNotMatch(timeline, /setOpen\(true\)/);
  assert.doesNotMatch(page, /workflowStatus[^\n]+setDetailPanel/);
  assert.doesNotMatch(page, /setConversationPanel/);
});

test("mode selection uses progressive disclosure and plain-language presets", () => {
  const picker = source("../src/components/composer/mode-picker.tsx");
  for (const mode of ["auto", "writing", "polish"]) assert.match(picker, new RegExp(`value: "${mode}"`));
  assert.match(picker, /value === "guided"/);
  assert.match(picker, /生成前先确认提纲/);
  assert.match(picker, /高级设置/);
  assert.match(picker, /资料要求/);
  assert.match(picker, /严格验证/);
  assert.doesNotMatch(picker, />执行策略<|>验收强度</);
});
