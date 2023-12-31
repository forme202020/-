# 系统验证和检查

在部署完跨链系统之后，通常需要检查或监控系统的运行状态，这里提供一些检查和监控BitXHub系统的方法。

## 节点进程状态验证

可以通过ps命令查看节点进程的运行状态，示例如下：

```
ps aux|grep bitxhub
ps aux|grep pier
```

## 节点日志检查

如果是在终端前台启动的节点，那么日志会实时打印在终端上，观察其无报错即可；

如果是通过nohup等后台启动的节点，在节点主配置目录的logs文件夹中就是节点的日志文件，打开即可检查日志，一般情况下除了出块，bitxhub节点之间会定时相互`ping`其它节点并返回延时信息，可以简单看到节点集群之间的网络状态。

