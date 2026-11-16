<script setup lang="ts">
import { useAuthStore } from '../stores/auth'
import { platform } from '../stores/platform'
const auth = useAuthStore()
</script>

<template>
  <section class="welcome-section">
    <p class="eyebrow">用户空间</p>
    <h1>欢迎，{{ auth.user?.display_name || auth.user?.username }}。</h1>
    <p class="muted">你已成功登录{{ platform.name }}。</p>
    <el-card shadow="never" class="section-gap">
      <h2>当前版本说明</h2>
      <p>平台已支持套题与小题管理、静态 Flag 提交、排行榜和标准环境包校验。</p>
      <el-alert title="开题功能尚未开放" description="实际容器开题、销毁和网络地址分配尚未接入。请从竞赛与训练列表选择已发布的题目。" type="info" :closable="false" show-icon />
      <p class="muted">如需调整账号信息，请联系管理员；使用完毕后请点击右上方“退出登录”。</p>
    </el-card>
  </section>
</template>
