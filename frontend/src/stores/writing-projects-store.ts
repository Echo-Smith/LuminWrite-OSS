import { create } from "zustand";
import { useAuthStore } from "@/stores/auth-store";

// ─── 类型定义 ─────────────────────────────────────────────

export type ProjectStatus = "drafting" | "writing" | "completed" | "archived";

export interface WritingProject {
  id: string;
  title: string;
  description: string;
  status: ProjectStatus;
  
  // 写作进度
  wordCount: number;
  targetWordCount: number;
  lastEditedAt: string;
  
  // 元数据
  createdAt: string;
  updatedAt: string;
  tags: string[];
  
  // 关联资源
  conversationId: string; // 对应的 /write 会话
  materialIds: string[];  // 挂载的知识卡片
  
  // 可选：模板信息
  templateId?: string;
  templateName?: string;
}

export interface CreateProjectInput {
  title: string;
  description?: string;
  targetWordCount?: number;
  tags?: string[];
  templateId?: string;
}

export interface ProjectTemplate {
  id: string;
  name: string;
  description: string;
  targetWordCount: number;
  suggestedTags: string[];
  icon: string;
}

// ─── Store 定义 ───────────────────────────────────────────

interface WritingProjectsState {
  projects: WritingProject[];
  loading: boolean;
  error: string | null;
  selectedProjectId: string | null;
  
  // 读取操作
  fetchProjects: () => Promise<void>;
  getProjectById: (id: string) => WritingProject | undefined;
  
  // 写入操作
  createProject: (data: CreateProjectInput) => Promise<WritingProject>;
  updateProject: (id: string, data: Partial<WritingProject>) => Promise<void>;
  deleteProject: (id: string) => Promise<void>;
  archiveProject: (id: string) => Promise<void>;
  
  // UI 状态
  setSelectedProject: (id: string | null) => void;
  
  // 模板
  templates: ProjectTemplate[];
  fetchTemplates: () => void;
}

// 内置模板
const BUILTIN_TEMPLATES: ProjectTemplate[] = [
  {
    id: "tech-blog",
    name: "技术博客",
    description: "技术分享、教程、实践经验",
    targetWordCount: 3000,
    suggestedTags: ["技术", "教程"],
    icon: "💻",
  },
  {
    id: "academic-review",
    name: "学术综述",
    description: "文献综述、研究总结",
    targetWordCount: 8000,
    suggestedTags: ["学术", "综述"],
    icon: "📚",
  },
  {
    id: "product-docs",
    name: "产品文档",
    description: "产品说明、使用指南",
    targetWordCount: 5000,
    suggestedTags: ["文档", "产品"],
    icon: "📋",
  },
  {
    id: "fiction-chapter",
    name: "小说章节",
    description: "虚构故事、章节内容",
    targetWordCount: 5000,
    suggestedTags: ["小说", "创作"],
    icon: "📖",
  },
  {
    id: "news-report",
    name: "新闻报道",
    description: "新闻稿、事件报道",
    targetWordCount: 1500,
    suggestedTags: ["新闻", "报道"],
    icon: "📰",
  },
];

// 从后端 EditorialTask 映射到前端 WritingProject
function mapTaskToProject(task: any): WritingProject {
  // 简化状态映射
  const statusMapping: Record<string, ProjectStatus> = {
    draft: "drafting",
    pending_approval: "drafting",
    research: "writing",
    writing: "writing",
    review: "writing",
    pending_publish: "completed",
    published: "completed",
    archived: "archived",
  };
  
  return {
    id: task.id,
    title: task.title,
    description: task.description || "",
    status: statusMapping[task.status] || "drafting",
    wordCount: task.word_count || 0,
    targetWordCount: task.target_word_count || Math.floor(task.token_budget / 4) || 3000,
    lastEditedAt: task.updated_at,
    createdAt: task.created_at,
    updatedAt: task.updated_at,
    tags: task.tags || [],
    conversationId: task.conversation_id || "",
    materialIds: task.material_ids || [],
    templateId: task.template_id,
    templateName: task.template_name,
  };
}

// 从前端 WritingProject 映射回后端 EditorialTask
function mapProjectToTask(project: Partial<WritingProject>): any {
  const reverseStatusMapping: Record<ProjectStatus, string> = {
    drafting: "draft",
    writing: "writing",
    completed: "published",
    archived: "archived",
  };
  
  return {
    title: project.title,
    description: project.description,
    status: project.status ? reverseStatusMapping[project.status] : undefined,
    word_count: project.wordCount,
    target_word_count: project.targetWordCount,
    tags: project.tags,
    conversation_id: project.conversationId,
    material_ids: project.materialIds,
    template_id: project.templateId,
    template_name: project.templateName,
  };
}

export const useWritingProjectsStore = create<WritingProjectsState>((set, get) => ({
  projects: [],
  loading: false,
  error: null,
  selectedProjectId: null,
  templates: BUILTIN_TEMPLATES,

  fetchProjects: async () => {
    set({ loading: true, error: null });
    try {
      const token = useAuthStore.getState().token;
      if (!token) throw new Error("未登录");

      const response = await fetch("/api/editorial/tasks", {
        headers: {
          Authorization: `Bearer ${token}`,
        },
      });

      if (!response.ok) {
        throw new Error(`获取项目列表失败: ${response.statusText}`);
      }

      const data = await response.json();
      const projects = (data.tasks || []).map(mapTaskToProject);
      set({ projects, loading: false });
    } catch (error) {
      console.error("fetchProjects error:", error);
      set({ 
        error: error instanceof Error ? error.message : "获取项目列表失败",
        loading: false 
      });
    }
  },

  getProjectById: (id: string) => {
    return get().projects.find((p) => p.id === id);
  },

  createProject: async (data: CreateProjectInput) => {
    set({ loading: true, error: null });
    try {
      const token = useAuthStore.getState().token;
      if (!token) throw new Error("未登录");

      const template = data.templateId 
        ? BUILTIN_TEMPLATES.find(t => t.id === data.templateId)
        : undefined;

      const payload = {
        title: data.title,
        description: data.description || "",
        target_word_count: data.targetWordCount || template?.targetWordCount || 3000,
        tags: data.tags || template?.suggestedTags || [],
        template_id: data.templateId,
        template_name: template?.name,
        status: "draft",
        assignee_type: "writing_agent",
        token_budget: (data.targetWordCount || 3000) * 4, // 粗略估算
      };

      const response = await fetch("/api/editorial/tasks", {
        method: "POST",
        headers: {
          "Content-Type": "application/json",
          Authorization: `Bearer ${token}`,
        },
        body: JSON.stringify(payload),
      });

      if (!response.ok) {
        throw new Error(`创建项目失败: ${response.statusText}`);
      }

      const result = await response.json();
      const newProject = mapTaskToProject(result.task);
      
      set((state) => ({
        projects: [newProject, ...state.projects],
        loading: false,
      }));

      return newProject;
    } catch (error) {
      console.error("createProject error:", error);
      const errorMsg = error instanceof Error ? error.message : "创建项目失败";
      set({ error: errorMsg, loading: false });
      throw new Error(errorMsg);
    }
  },

  updateProject: async (id: string, data: Partial<WritingProject>) => {
    set({ loading: true, error: null });
    try {
      const token = useAuthStore.getState().token;
      if (!token) throw new Error("未登录");

      const payload = mapProjectToTask(data);

      const response = await fetch(`/api/editorial/tasks/${id}`, {
        method: "PATCH",
        headers: {
          "Content-Type": "application/json",
          Authorization: `Bearer ${token}`,
        },
        body: JSON.stringify(payload),
      });

      if (!response.ok) {
        throw new Error(`更新项目失败: ${response.statusText}`);
      }

      const result = await response.json();
      const updatedProject = mapTaskToProject(result.task);

      set((state) => ({
        projects: state.projects.map((p) => (p.id === id ? updatedProject : p)),
        loading: false,
      }));
    } catch (error) {
      console.error("updateProject error:", error);
      set({ 
        error: error instanceof Error ? error.message : "更新项目失败",
        loading: false 
      });
      throw error;
    }
  },

  deleteProject: async (id: string) => {
    set({ loading: true, error: null });
    try {
      const token = useAuthStore.getState().token;
      if (!token) throw new Error("未登录");

      const response = await fetch(`/api/editorial/tasks/${id}`, {
        method: "DELETE",
        headers: {
          Authorization: `Bearer ${token}`,
        },
      });

      if (!response.ok) {
        throw new Error(`删除项目失败: ${response.statusText}`);
      }

      set((state) => ({
        projects: state.projects.filter((p) => p.id !== id),
        loading: false,
      }));
    } catch (error) {
      console.error("deleteProject error:", error);
      set({ 
        error: error instanceof Error ? error.message : "删除项目失败",
        loading: false 
      });
      throw error;
    }
  },

  archiveProject: async (id: string) => {
    await get().updateProject(id, { status: "archived" });
  },

  setSelectedProject: (id: string | null) => {
    set({ selectedProjectId: id });
  },

  fetchTemplates: () => {
    // 当前使用内置模板，未来可扩展为从后端获取
    set({ templates: BUILTIN_TEMPLATES });
  },
}));
