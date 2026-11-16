<template>
  <div class="login-wrap">
    <div class="login-card" style="text-align: center">
      <div class="login-logo" style="margin: 0 auto 20px">?</div>
      <div class="eyebrow" style="margin-bottom: 10px">404 / Not found</div>
      <h1>页面不存在</h1>
      <p>你访问的页面不存在，或已被移动。</p>
      <button class="btn btn-primary" @click="$router.back()">返回上一页</button>
    </div>
  </div>
</template>
