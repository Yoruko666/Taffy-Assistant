package com.taffy.client.ui.login

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.widthIn
import androidx.compose.material3.Button
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.text.input.PasswordVisualTransformation
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.lifecycle.viewmodel.compose.viewModel
import com.taffy.client.TaffyApplication
import androidx.compose.ui.platform.LocalContext

/** 登录 / 注册页。同一页面通过 [LoginUiState.Mode] 切换模式。*/
@Composable
fun LoginScreen(onLoggedIn: () -> Unit) {
    val context = LocalContext.current
    val app = context.applicationContext as TaffyApplication
    val vm: LoginViewModel = viewModel(
        factory = LoginViewModel.Factory(app.apiClient, app.tokenStore),
    )
    val state by vm.uiState.collectAsStateWithLifecycle()

    LaunchedEffect(state.loggedIn) {
        if (state.loggedIn) onLoggedIn()
    }

    Surface(modifier = Modifier.fillMaxSize(), color = MaterialTheme.colorScheme.background) {
        Column(
            modifier = Modifier
                .fillMaxSize()
                .padding(horizontal = 24.dp),
            horizontalAlignment = Alignment.CenterHorizontally,
            verticalArrangement = Arrangement.Center,
        ) {
            Text(
                text = "小菲 Taffy",
                fontSize = 32.sp,
                fontWeight = FontWeight.Bold,
                color = MaterialTheme.colorScheme.primary,
            )
            Text(
                text = if (state.mode == LoginUiState.Mode.LOGIN) "登录到智能家居" else "创建新账号",
                fontSize = 14.sp,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )

            Spacer(Modifier.height(32.dp))

            OutlinedTextField(
                value = state.phone,
                onValueChange = vm::onPhoneChange,
                label = { Text("手机号") },
                singleLine = true,
                modifier = Modifier
                    .fillMaxWidth()
                    .widthIn(max = 360.dp),
            )
            Spacer(Modifier.height(12.dp))

            OutlinedTextField(
                value = state.password,
                onValueChange = vm::onPasswordChange,
                label = { Text("密码（至少 6 位）") },
                singleLine = true,
                visualTransformation = PasswordVisualTransformation(),
                modifier = Modifier
                    .fillMaxWidth()
                    .widthIn(max = 360.dp),
            )

            if (state.mode == LoginUiState.Mode.REGISTER) {
                Spacer(Modifier.height(12.dp))
                OutlinedTextField(
                    value = state.nickname,
                    onValueChange = vm::onNicknameChange,
                    label = { Text("昵称（可选）") },
                    singleLine = true,
                    modifier = Modifier.fillMaxWidth().widthIn(max = 360.dp),
                )
                Spacer(Modifier.height(12.dp))
                OutlinedTextField(
                    value = state.email,
                    onValueChange = vm::onEmailChange,
                    label = { Text("邮箱（可选）") },
                    singleLine = true,
                    modifier = Modifier.fillMaxWidth().widthIn(max = 360.dp),
                )
            }

            if (state.errorMessage != null) {
                Spacer(Modifier.height(12.dp))
                Text(
                    text = state.errorMessage!!,
                    color = MaterialTheme.colorScheme.error,
                    fontSize = 13.sp,
                )
            }

            Spacer(Modifier.height(24.dp))

            Button(
                onClick = vm::submit,
                enabled = state.canSubmit,
                modifier = Modifier
                    .fillMaxWidth()
                    .widthIn(max = 360.dp)
                    .height(48.dp),
            ) {
                if (state.loading) {
                    CircularProgressIndicator(
                        strokeWidth = 2.dp,
                        modifier = Modifier.height(20.dp),
                        color = MaterialTheme.colorScheme.onPrimary,
                    )
                } else {
                    Text(
                        text = if (state.mode == LoginUiState.Mode.LOGIN) "登录" else "注册",
                        fontSize = 16.sp,
                    )
                }
            }

            Spacer(Modifier.height(8.dp))

            TextButton(
                onClick = vm::toggleMode,
                enabled = !state.loading,
            ) {
                Text(
                    if (state.mode == LoginUiState.Mode.LOGIN) "没有账号？去注册"
                    else "已有账号？去登录",
                    fontSize = 13.sp,
                )
            }
        }
    }
}
