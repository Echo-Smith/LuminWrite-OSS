/**
 * 写作项目管理器 - 新的工作台主页
 * 
 * 替代原有的 EditorialBoard，提供轻量级的个人写作项目管理功能
 */
import { useEffect, useState } from "react";
import { useNavigate } from "react-router-dom";
import {
  useWritingProjectsStore,
  type WritingProject,
  type ProjectStatus,
  type ProjectTemplate,
} from "@/stores/writing-projects-store";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Textarea } from "@/components/ui/textarea";
import { Badge } from "@/components/ui/badge";
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from "@/components/ui/dialog";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import {
  FileText,
  Plus,
  MoreVertical,
  Archive,
  Trash2,
  Clock,
  Tag,
  Layout,
  ArrowLeft,
} from "lucide-react";
import { cn } from "@/lib/utils";
import { formatDistanceToNow } from "date-fns";
import { zhCN } from "date-fns/locale";

const STATUS_LABELS: Record<ProjectStatus, string> = {
  drafting: "构思中",
  writing: "写作中",
  completed: "已完成",
  archived: "已归档",
};

const STATUS_COLORS: Record<ProjectStatus, string> = {
  drafting: "bg-slate-500",
  writing: "bg-blue-500",
  completed: "bg-green-500",
  archived: "bg-gray-400",
};

type FilterStatus = "all" | ProjectStatus;

export function WritingProjectsPage() {
  const navigate = useNavigate();
  const {
    projects,
    loading,
    selectedProjectId,
    templates,
    fetchProjects,
    createProject,
    updateProject,
    deleteProject,
    archiveProject,
    setSelectedProject,
    fetchTemplates,
  } = useWritingProjectsStore();

  const [filter, setFilter] = useState<FilterStatus>("all");
  const [showCreateDialog, setShowCreateDialog] = useState(false);

  useEffect(() => {
    fetchProjects();
    fetchTemplates();
  }, [fetchProjects, fetchTemplates]);

  const filteredProjects =
    filter === "all" ? projects : projects.filter((p) => p.status === filter);

  const selectedProject = projects.find((p) => p.id === selectedProjectId);

  return (
    <div className="flex h-[calc(100vh-3.5rem)] bg-background">
      {/* 主内容区 */}
      <div className="flex-1 flex flex-col overflow-hidden">
        {/* 顶部工具栏 */}
        <div className="flex items-center justify-between border-b px-6 py-3 bg-surface shrink-0">
          <div className="flex items-center gap-3">
            <button
              onClick={() => navigate("/write")}
              className="flex h-8 w-8 items-center justify-center rounded-lg hover:bg-accent transition-ui text-muted-foreground hover:text-foreground"
              title="返回写作"
            >
              <ArrowLeft className="h-4 w-4" />
            </button>
            <Layout className="h-5 w-5 text-primary" />
            <h1 className="text-lg font-semibold">写作项目</h1>
          </div>

          <div className="flex items-center gap-2">
            <CreateProjectDialog
              templates={templates}
              onCreateProject={async (data) => {
                const project = await createProject(data);
                navigate(`/write?session=${project.conversationId}`);
              }}
              open={showCreateDialog}
              onOpenChange={setShowCreateDialog}
            />
            <Button onClick={() => setShowCreateDialog(true)}>
              <Plus className="h-4 w-4 mr-1" />
              新建项目
            </Button>
          </div>
        </div>

        {/* 筛选栏 */}
        <div className="flex items-center gap-2 px-6 py-3 border-b bg-surface/50">
          <button
            onClick={() => setFilter("all")}
            className={cn(
              "px-3 py-1.5 text-sm rounded-md transition-colors",
              filter === "all"
                ? "bg-primary/15 text-primary font-medium"
                : "text-muted-foreground hover:text-foreground hover:bg-accent"
            )}
          >
            全部 ({projects.length})
          </button>
          <button
            onClick={() => setFilter("writing")}
            className={cn(
              "px-3 py-1.5 text-sm rounded-md transition-colors",
              filter === "writing"
                ? "bg-primary/15 text-primary font-medium"
                : "text-muted-foreground hover:text-foreground hover:bg-accent"
            )}
          >
            进行中 ({projects.filter((p) => p.status === "writing" || p.status === "drafting").length})
          </button>
          <button
            onClick={() => setFilter("completed")}
            className={cn(
              "px-3 py-1.5 text-sm rounded-md transition-colors",
              filter === "completed"
                ? "bg-primary/15 text-primary font-medium"
                : "text-muted-foreground hover:text-foreground hover:bg-accent"
            )}
          >
            已完成 ({projects.filter((p) => p.status === "completed").length})
          </button>
          <button
            onClick={() => setFilter("archived")}
            className={cn(
              "px-3 py-1.5 text-sm rounded-md transition-colors",
              filter === "archived"
                ? "bg-primary/15 text-primary font-medium"
                : "text-muted-foreground hover:text-foreground hover:bg-accent"
            )}
          >
            已归档 ({projects.filter((p) => p.status === "archived").length})
          </button>
        </div>

        {/* 项目列表 */}
        <div className="flex-1 overflow-y-auto p-6">
          {loading && projects.length === 0 ? (
            <div className="flex items-center justify-center h-full text-muted-foreground">
              <div className="text-center">
                <Clock className="h-12 w-12 mx-auto mb-2 animate-spin" />
                <p>加载中...</p>
              </div>
            </div>
          ) : filteredProjects.length === 0 ? (
            <div className="flex items-center justify-center h-full text-muted-foreground">
              <div className="text-center">
                <FileText className="h-12 w-12 mx-auto mb-2 opacity-50" />
                <p className="mb-4">暂无项目</p>
                <Button onClick={() => setShowCreateDialog(true)}>
                  <Plus className="h-4 w-4 mr-1" />
                  创建第一个项目
                </Button>
              </div>
            </div>
          ) : (
            <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 xl:grid-cols-4 gap-4">
              {filteredProjects.map((project) => (
                <ProjectCard
                  key={project.id}
                  project={project}
                  onClick={() => {
                    setSelectedProject(project.id);
                    navigate(`/write?session=${project.conversationId}`);
                  }}
                  onArchive={() => archiveProject(project.id)}
                  onDelete={() => {
                    if (confirm(`确定删除项目「${project.title}」吗？此操作不可撤销。`)) {
                      deleteProject(project.id);
                    }
                  }}
                  isSelected={selectedProjectId === project.id}
                />
              ))}
            </div>
          )}
        </div>
      </div>

      {/* 详情面板（可选，暂时隐藏） */}
      {selectedProject && (
        <ProjectDetailPanel
          project={selectedProject}
          onClose={() => setSelectedProject(null)}
          onUpdate={(data) => updateProject(selectedProject.id, data)}
        />
      )}
    </div>
  );
}

// ─── 项目卡片组件 ─────────────────────────────────────────

interface ProjectCardProps {
  project: WritingProject;
  onClick: () => void;
  onArchive: () => void;
  onDelete: () => void;
  isSelected: boolean;
}

function ProjectCard({ project, onClick, onArchive, onDelete, isSelected }: ProjectCardProps) {
  const progress = project.targetWordCount > 0 
    ? Math.min(100, (project.wordCount / project.targetWordCount) * 100)
    : 0;

  return (
    <div
      className={cn(
        "group relative bg-surface border rounded-xl p-4 hover:shadow-lg transition-all cursor-pointer",
        isSelected && "ring-2 ring-primary"
      )}
      onClick={onClick}
    >
      {/* 状态标签 */}
      <div className="flex items-center justify-between mb-3">
        <Badge className={cn("text-xs", STATUS_COLORS[project.status])}>
          {STATUS_LABELS[project.status]}
        </Badge>
        
        <DropdownMenu>
          <DropdownMenuTrigger asChild onClick={(e: React.MouseEvent) => e.stopPropagation()}>
            <button className="opacity-0 group-hover:opacity-100 transition-opacity p-1 hover:bg-accent rounded">
              <MoreVertical className="h-4 w-4" />
            </button>
          </DropdownMenuTrigger>
          <DropdownMenuContent align="end">
            <DropdownMenuItem onClick={(e: React.MouseEvent) => { e.stopPropagation(); onArchive(); }}>
              <Archive className="h-4 w-4 mr-2" />
              归档
            </DropdownMenuItem>
            <DropdownMenuItem 
              onClick={(e: React.MouseEvent) => { e.stopPropagation(); onDelete(); }}
              className="text-red-600"
            >
              <Trash2 className="h-4 w-4 mr-2" />
              删除
            </DropdownMenuItem>
          </DropdownMenuContent>
        </DropdownMenu>
      </div>

      {/* 标题 */}
      <h3 className="font-semibold text-base mb-2 line-clamp-2">{project.title}</h3>

      {/* 描述 */}
      {project.description && (
        <p className="text-sm text-muted-foreground mb-3 line-clamp-2">
          {project.description}
        </p>
      )}

      {/* 进度条 */}
      <div className="mb-3">
        <div className="flex items-center justify-between text-xs text-muted-foreground mb-1">
          <span>{project.wordCount.toLocaleString()} 字</span>
          <span>{project.targetWordCount.toLocaleString()} 字</span>
        </div>
        <div className="h-1.5 bg-accent rounded-full overflow-hidden">
          <div
            className="h-full bg-primary transition-all"
            style={{ width: `${progress}%` }}
          />
        </div>
      </div>

      {/* 元数据 */}
      <div className="flex items-center gap-3 text-xs text-muted-foreground mb-3">
        <div className="flex items-center gap-1">
          <Clock className="h-3 w-3" />
          <span>
            {formatDistanceToNow(new Date(project.lastEditedAt), {
              addSuffix: true,
              locale: zhCN,
            })}
          </span>
        </div>
      </div>

      {/* 标签 */}
      {project.tags.length > 0 && (
        <div className="flex items-center gap-1 flex-wrap">
          {project.tags.slice(0, 3).map((tag) => (
            <Badge key={tag} variant="outline" className="text-xs">
              {tag}
            </Badge>
          ))}
          {project.tags.length > 3 && (
            <span className="text-xs text-muted-foreground">+{project.tags.length - 3}</span>
          )}
        </div>
      )}
    </div>
  );
}

// ─── 创建项目对话框 ─────────────────────────────────────────

interface CreateProjectDialogProps {
  templates: ProjectTemplate[];
  onCreateProject: (data: any) => Promise<void>;
  open: boolean;
  onOpenChange: (open: boolean) => void;
}

function CreateProjectDialog({ templates, onCreateProject, open, onOpenChange }: CreateProjectDialogProps) {
  const [title, setTitle] = useState("");
  const [description, setDescription] = useState("");
  const [targetWordCount, setTargetWordCount] = useState<number | undefined>(undefined);
  const [tags, setTags] = useState<string[]>([]);
  const [selectedTemplateId, setSelectedTemplateId] = useState<string | undefined>(undefined);
  const [tagInput, setTagInput] = useState("");
  const [creating, setCreating] = useState(false);

  const handleTemplateSelect = (template: ProjectTemplate) => {
    setSelectedTemplateId(template.id);
    setTargetWordCount(template.targetWordCount);
    setTags(template.suggestedTags);
  };

  const handleCreate = async () => {
    if (!title.trim()) {
      alert("请输入项目标题");
      return;
    }

    setCreating(true);
    try {
      await onCreateProject({
        title,
        description,
        targetWordCount,
        tags,
        templateId: selectedTemplateId,
      });

      // 重置表单
      setTitle("");
      setDescription("");
      setTargetWordCount(undefined);
      setTags([]);
      setSelectedTemplateId(undefined);
      setTagInput("");
      onOpenChange(false);
    } catch (error) {
      console.error("创建项目失败:", error);
      alert("创建项目失败，请重试");
    } finally {
      setCreating(false);
    }
  };

  const addTag = () => {
    if (tagInput.trim() && !tags.includes(tagInput.trim())) {
      setTags([...tags, tagInput.trim()]);
      setTagInput("");
    }
  };

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-[600px]">
        <DialogHeader>
          <DialogTitle>创建新项目</DialogTitle>
        </DialogHeader>

        <div className="space-y-4 mt-4">
          {/* 模板选择 */}
          <div>
            <Label className="text-sm font-medium mb-2 block">选择模板（可选）</Label>
            <div className="grid grid-cols-2 gap-2">
              {templates.map((template) => (
                <button
                  key={template.id}
                  onClick={() => handleTemplateSelect(template)}
                  className={cn(
                    "p-3 border rounded-lg text-left hover:bg-accent transition-colors",
                    selectedTemplateId === template.id && "ring-2 ring-primary bg-accent"
                  )}
                >
                  <div className="text-xl mb-1">{template.icon}</div>
                  <div className="font-medium text-sm">{template.name}</div>
                  <div className="text-xs text-muted-foreground">{template.description}</div>
                </button>
              ))}
            </div>
          </div>

          {/* 标题 */}
          <div>
            <Label htmlFor="title" className="text-sm font-medium mb-2 block">
              项目标题 <span className="text-red-500">*</span>
            </Label>
            <Input
              id="title"
              value={title}
              onChange={(e) => setTitle(e.target.value)}
              placeholder="例如：量子计算综述"
            />
          </div>

          {/* 描述 */}
          <div>
            <Label htmlFor="description" className="text-sm font-medium mb-2 block">
              简要描述（可选）
            </Label>
            <Textarea
              id="description"
              value={description}
              onChange={(e) => setDescription(e.target.value)}
              placeholder="描述项目的主要内容和目标"
              rows={3}
            />
          </div>

          {/* 目标字数 */}
          <div>
            <Label htmlFor="wordCount" className="text-sm font-medium mb-2 block">
              目标字数（可选）
            </Label>
            <Input
              id="wordCount"
              type="number"
              value={targetWordCount || ""}
              onChange={(e) => setTargetWordCount(e.target.value ? parseInt(e.target.value) : undefined)}
              placeholder="3000"
            />
          </div>

          {/* 标签 */}
          <div>
            <Label className="text-sm font-medium mb-2 block">标签（可选）</Label>
            <div className="flex gap-2 mb-2">
              <Input
                value={tagInput}
                onChange={(e) => setTagInput(e.target.value)}
                onKeyDown={(e) => e.key === "Enter" && addTag()}
                placeholder="输入标签后按回车"
              />
              <Button type="button" variant="outline" onClick={addTag}>
                <Plus className="h-4 w-4" />
              </Button>
            </div>
            <div className="flex flex-wrap gap-1">
              {tags.map((tag) => (
                <Badge key={tag} variant="secondary">
                  {tag}
                  <button
                    onClick={() => setTags(tags.filter((t) => t !== tag))}
                    className="ml-1 hover:text-red-600"
                  >
                    ×
                  </button>
                </Badge>
              ))}
            </div>
          </div>

          {/* 操作按钮 */}
          <div className="flex justify-end gap-2 pt-4">
            <Button variant="outline" onClick={() => onOpenChange(false)} disabled={creating}>
              取消
            </Button>
            <Button onClick={handleCreate} disabled={creating || !title.trim()}>
              {creating ? "创建中..." : "创建项目"}
            </Button>
          </div>
        </div>
      </DialogContent>
    </Dialog>
  );
}

// ─── 项目详情面板（暂时未使用） ─────────────────────────────

interface ProjectDetailPanelProps {
  project: WritingProject;
  onClose: () => void;
  onUpdate: (data: Partial<WritingProject>) => void;
}

function ProjectDetailPanel({ project, onClose, onUpdate }: ProjectDetailPanelProps) {
  return (
    <div className="w-80 border-l bg-surface overflow-y-auto p-4">
      <div className="flex items-center justify-between mb-4">
        <h2 className="font-semibold">项目详情</h2>
        <button onClick={onClose} className="text-muted-foreground hover:text-foreground">
          ×
        </button>
      </div>

      <div className="space-y-4">
        <div>
          <Label className="text-sm font-medium">标题</Label>
          <p className="text-sm mt-1">{project.title}</p>
        </div>

        {project.description && (
          <div>
            <Label className="text-sm font-medium">描述</Label>
            <p className="text-sm mt-1 text-muted-foreground">{project.description}</p>
          </div>
        )}

        <div>
          <Label className="text-sm font-medium">状态</Label>
          <Badge className={cn("mt-1", STATUS_COLORS[project.status])}>
            {STATUS_LABELS[project.status]}
          </Badge>
        </div>

        <div>
          <Label className="text-sm font-medium">写作进度</Label>
          <div className="mt-2">
            <div className="flex justify-between text-sm mb-1">
              <span>{project.wordCount.toLocaleString()} 字</span>
              <span>{project.targetWordCount.toLocaleString()} 字</span>
            </div>
            <div className="h-2 bg-accent rounded-full overflow-hidden">
              <div
                className="h-full bg-primary"
                style={{
                  width: `${Math.min(100, (project.wordCount / project.targetWordCount) * 100)}%`,
                }}
              />
            </div>
          </div>
        </div>

        {project.tags.length > 0 && (
          <div>
            <Label className="text-sm font-medium">标签</Label>
            <div className="flex flex-wrap gap-1 mt-1">
              {project.tags.map((tag) => (
                <Badge key={tag} variant="outline" className="text-xs">
                  {tag}
                </Badge>
              ))}
            </div>
          </div>
        )}

        <div>
          <Label className="text-sm font-medium">创建时间</Label>
          <p className="text-sm mt-1 text-muted-foreground">
            {new Date(project.createdAt).toLocaleString("zh-CN")}
          </p>
        </div>

        <div>
          <Label className="text-sm font-medium">最后编辑</Label>
          <p className="text-sm mt-1 text-muted-foreground">
            {formatDistanceToNow(new Date(project.lastEditedAt), {
              addSuffix: true,
              locale: zhCN,
            })}
          </p>
        </div>
      </div>
    </div>
  );
}

// Helper: Label 组件（如果未定义）
function Label({ children, htmlFor, className }: { children: React.ReactNode; htmlFor?: string; className?: string }) {
  return (
    <label htmlFor={htmlFor} className={cn("text-sm font-medium", className)}>
      {children}
    </label>
  );
}
