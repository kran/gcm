<template>
    <!-- 初始化加载 -->
    <div v-if="phase === 'loading'" class="panel-init-loading">
        <div class="panel-init-spinner"></div>
        <div class="panel-init-text">gcm Admin</div>
    </div>

    <!-- 登录 -->
    <div v-else-if="phase === 'login'" class="panel-login-wrapper">
        <div class="panel-login-card">
            <div class="panel-login-logo">
                <span>gcm</span>
            </div>
            <div class="panel-login-subtitle">{{ siteName }} · 内容管理</div>
            <el-form @submit.prevent="doLogin" class="panel-login-form">
                <el-input v-model="loginForm.username" placeholder="用户名" size="large">
                    <template #prefix><el-icon><User /></el-icon></template>
                </el-input>
                <el-input v-model="loginForm.password" type="password" placeholder="密码" size="large"
                    show-password @keyup.enter="doLogin">
                    <template #prefix><el-icon><Lock /></el-icon></template>
                </el-input>
                <el-alert v-if="loginForm.error" :title="loginForm.error" type="error"
                    :closable="false" show-icon style="margin-bottom:4px;" />
                <el-button type="primary" :loading="loginForm.loading" size="large" style="width:100%"
                    @click="doLogin">登 录</el-button>
            </el-form>
        </div>
    </div>

    <!-- 主布局: Jira 骨架 (顶部横栏贯穿 + 下方侧栏/内容) -->
    <div v-else class="panel-shell">
        <header class="panel-topbar">
            <span class="topbar-logo">gcm</span>
            <span class="topbar-title">{{ siteName }}{{ siteName ? ' · ' : '' }}{{ pageTitle }}</span>
            <div class="topbar-right">
                <el-dropdown>
                    <span class="topbar-user">
                        <el-icon><User /></el-icon>
                        {{ user?.username || 'admin' }}
                    </span>
                    <template #dropdown>
                        <el-dropdown-menu>
                            <el-dropdown-item @click="doLogout">退出登录</el-dropdown-item>
                        </el-dropdown-menu>
                    </template>
                </el-dropdown>
            </div>
        </header>

        <div class="panel-body">
            <nav class="panel-rail">
                <el-tooltip v-for="item in menuData" :key="item.key" :content="item.label"
                    placement="right">
                    <div class="rail-item" :class="{ active: route.name === item.route }"
                        @click="router.push({ name: item.route })">
                        <el-icon :size="19"><component :is="item.icon" /></el-icon>
                    </div>
                </el-tooltip>
            </nav>

            <div class="panel-main">
                <div class="panel-content">
                    <div class="content-card">
                        <router-view />
                    </div>
                </div>
            </div>
        </div>
    </div>
</template>
<script>
import { ref, reactive, computed, onMounted, provide } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import $api from '$api'

export default {
    setup() {
        var route = useRoute()
        var router = useRouter()
        var menuData = ref(window.AppConfig.menu) // ref: 插件面板动态 push 后 UI 刷新
        var defaultPage = window.AppConfig.defaultPage

        var phase = ref('loading')
        var user = ref(null)
        provide('user', user)

        var loginForm = reactive({ username: '', password: '', loading: false, error: '' })

        var siteName = computed(function () { return user.value?.site || '' })

        var pageTitle = computed(function () {
            var name = route.name
            var list = menuData.value
            for (var i = 0; i < list.length; i++) {
                if (list[i].route === name) return list[i].label
            }
            return name || '首页'
        })

        // 防止 401 触发登出时, 登出请求自身再 401 造成递归
        var _loggingOut = false
        function doLogout() {
            if (_loggingOut) return
            _loggingOut = true
            $api.logout().catch(function () {}).finally(function () { _loggingOut = false })
            user.value = null
            phase.value = 'login'
            router.push('/')
        }

        async function checkAuth() {
            try {
                user.value = await $api.me()
                phase.value = 'app'
            } catch (_) {
                phase.value = 'login'
            }
        }

        async function doLogin() {
            loginForm.loading = true
            loginForm.error = ''
            try {
                await $api.login(loginForm.username, loginForm.password)
                user.value = await $api.me()
                phase.value = 'app'
                if (defaultPage) router.push({ name: defaultPage })
            } catch (_) {
                loginForm.error = '用户名或密码错误'
            }
            loginForm.loading = false
        }

        onMounted(function () {
            Panel.onError(function (err) { if (err.status === 401) doLogout() })
            checkAuth()
        })

        return {
            phase: phase, user: user, siteName: siteName, pageTitle: pageTitle,
            menuData: menuData, loginForm: loginForm,
            route: route, router: router,
            doLogin: doLogin, doLogout: doLogout,
        }
    },
}
</script>
