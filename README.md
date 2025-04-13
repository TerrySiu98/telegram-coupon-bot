# Telegram Coupon Bot 🎟️🤖

一个用Telegram机器人发放v2board优惠码的程序

## ✨ 功能特性

- 对接v2board数据库 检测用户是否注册
- 把机器人加入官方群组 检测用户是否在群组中
- Telegram Bot 支持指令回复配置
- Web 后台管理 添加优惠码以及查看用户领取记录
- 可选设置用户领取天数间隔 捆绑邮箱和tg用户名

## 🚀 快速开始

### 使用 Docker

```bash
docker run -d \
  --name telegram-coupon-bot \
  -p 5656:5656 \
  terrysiu/telegram-coupon-bot:latest
```

### 使用 Docker Compose

如果你喜欢使用 Docker Compose，也可以：

```bash
git clone https://github.com/TerrySiu98/telegram-coupon-bot.git
cd telegram-coupon-bot
docker-compose up -d
```

## 📁 项目结构

```
.
├── main.go
├── templates/       # 前端页面模板
├── data/            # 本地数据库文件（SQLite）
├── Dockerfile
└── docker-compose.yml
```

## 📦 Docker 多架构支持

本项目使用 GitHub Actions 自动构建多架构镜像，兼容常见平台（如 Linux/amd64 与树莓派 arm64）。

## 💬 首次启动

访问后台管理界面：http://localhost:5656

默认账户密码admin 请登录后在系统设置里修改默认账户密码 。

填写Telegram Bot Token（@BotFather 创建你的Bot）

填写群组id（将@myidbot拉进群组 发送/getgroupid获取群组id）

填写数据库信息

重载配置

---

欢迎 Star ⭐ 或 Fork 本项目，有问题请提 Issue 🙌
