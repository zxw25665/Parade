<template>
  <div class="chat-panel">
    <div class="tab-bar">
      <button
        class="tab-btn"
        :class="{ active: activeTab === 'team' }"
        @click="activeTab = 'team'"
      >{{ $t('chat.teamChat') }}</button>
      <button
        class="tab-btn"
        :class="{ active: activeTab === 'private' }"
        @click="activeTab = 'private'"
      >{{ $t('chat.privateChat') }}</button>
    </div>
    <div class="chat-content">
      <TeamChat v-if="activeTab === 'team'" />
      <PrivateChat v-else />
    </div>
  </div>
</template>

<script setup>
import { ref } from 'vue'
import TeamChat from './TeamChat.vue'
import PrivateChat from './PrivateChat.vue'

const activeTab = ref('team')
</script>

<style scoped>
.chat-panel {
  display: flex;
  flex-direction: column;
  height: 100%;
}
.chat-content {
  flex: 1;
  display: flex;
  flex-direction: column;
  overflow: hidden;
}
</style>
