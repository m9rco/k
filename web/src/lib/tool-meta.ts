import {
  Palette,
  UserRoundCog,
  UserRoundPlus,
  Type,
  ImagePlus,
  Clapperboard,
  Scissors,
  Ruler,
  Search,
  Globe,
  LayoutGrid,
  Sparkles,
  AppWindow,
  MessageCircleQuestion,
  RotateCcw,
  Wrench,
  type LucideIcon,
} from "lucide-react";

// Maps an agent tool (optionally refined by intent) to a Lucide icon + a short
// Chinese label. Replaces the old emoji map — no colored emoji anywhere.
interface ToolMeta {
  icon: LucideIcon;
  title: string;
}

const TOOL_META: Record<string, ToolMeta> = {
  "edit_image:change_background": { icon: Palette, title: "换背景" },
  "edit_image:change_character": { icon: UserRoundCog, title: "换角色" },
  "edit_image:add_character":    { icon: UserRoundPlus, title: "增加角色" },
  "edit_image:change_text":      { icon: Type, title: "换文案" },
  "edit_image:retry":            { icon: RotateCcw, title: "重试生成" },
  edit_image:                    { icon: ImagePlus, title: "生成图片" },
  image_to_video:                { icon: Clapperboard, title: "生成视频" },
  crop_to_sizes:                 { icon: Scissors, title: "切尺寸" },
  list_platform_sizes:           { icon: Ruler, title: "查询尺寸" },
  crawl_game_assets:             { icon: Search, title: "爬取物料" },
  adapt_to_platform:             { icon: LayoutGrid, title: "适配尺寸" },
  search_images:                 { icon: Globe, title: "搜图" },
  generate_image_from_text:      { icon: Sparkles, title: "文字生图" },
  generate_icon:                 { icon: AppWindow, title: "生成图标" },
  clarify_intent:                { icon: MessageCircleQuestion, title: "确认意图" },
  web_search:                    { icon: Globe, title: "搜索资讯" },
};

export function toolMeta(name: string, args?: Record<string, unknown>): ToolMeta {
  const intent = args && typeof args.intent === "string" ? args.intent : "";
  const key = name === "edit_image" && intent ? `${name}:${intent}` : name;
  return TOOL_META[key] || TOOL_META[name] || { icon: Wrench, title: name || "工具" };
}

// toolSubtitle derives a readable secondary line from the tool's arguments.
export function toolSubtitle(name: string, args?: Record<string, unknown>): string {
  if (!args) return "";
  const s = (k: string) => (typeof args[k] === "string" ? (args[k] as string) : "");
  switch (name) {
    case "edit_image": {
      let sub = s("background_desc") || s("character_desc") || s("text_content");
      const refs = args.reference_asset_ids;
      if (Array.isArray(refs) && refs.length > 1) sub = (sub ? sub + " · " : "") + `参考 ${refs.length} 张`;
      return sub;
    }
    case "adapt_to_platform":
    case "crop_to_sizes": {
      const ids = args.size_ids;
      return Array.isArray(ids) && ids.length ? `${ids.length} 个尺寸` : "";
    }
    case "image_to_video":       return s("motion");
    case "generate_image_from_text": return s("description");
    case "generate_icon":        return s("icon_desc");
    case "search_images":
    case "web_search":           return s("query");
    case "crawl_game_assets":    return s("game");
    default:                     return "";
  }
}
